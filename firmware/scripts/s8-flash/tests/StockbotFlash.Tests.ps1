#requires -Version 5.1
BeforeAll {
    $ErrorActionPreference = 'Stop'
    Set-StrictMode -Version 2
    $root = Split-Path -Parent $PSScriptRoot
    $modulePath = if ($env:STOCKBOT_MODULE_PATH) { $env:STOCKBOT_MODULE_PATH } else { Join-Path $root 'StockbotFlash.psm1' }
    Import-Module $modulePath -Force
    $fixtures = Join-Path $PSScriptRoot 'fixtures'
    & python3 (Join-Path $fixtures 'gen_partition_table.py') | Out-Null
    & python3 (Join-Path $fixtures 'make_synthetic_flash.py') | Out-Null
    function script:LoadOtadataSectors([string]$file) {
        $flash = [System.IO.File]::ReadAllBytes((Join-Path $fixtures $file))
        $table = Read-PartitionTable -TableBytes $flash[0x8000..(0x8000 + 0xC00 - 1)]
        $ota = $table | Where-Object { $_.Name -eq 'otadata' } | Select-Object -First 1
        $blob = $flash[$ota.Offset..($ota.Offset + $ota.Size - 1)]
        return Read-Otadata -OtadataBytes $blob
    }
}

Describe 'Table allow-list provenance' {
    It 'cpp reference and csv compile are byte-identical' {
        $repo = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PSScriptRoot)))
        $cpp = [System.IO.File]::ReadAllBytes((Join-Path $repo 'firmware/bootloader-archive/atoms3lite-arduino-2.0.17/partitions.bin'))
        $csvBin = [System.IO.File]::ReadAllBytes((Join-Path $fixtures 'partitions_8mb_ota.bin'))
        $csvBin | Should -Be $cpp
        (Get-BytesSha256Hex -Bytes $csvBin) | Should -Be '8b3183d34d01892267fffd06e5a825c2d825d179551cf7b85db4e08223253dc1'
    }
    It 'parses committed reference table entries' {
        $bytes = [System.IO.File]::ReadAllBytes((Join-Path $fixtures 'partitions_8mb_ota.bin'))
        $entries = @(Read-PartitionTable -TableBytes $bytes)
        $entries.Count | Should -Be 5
        ($entries | Where-Object { $_.Name -eq 'nvs' }).Offset | Should -Be 0x9000
        ($entries | Where-Object { $_.Name -eq 'otadata' }).Offset | Should -Be 0xF000
        ($entries | Where-Object { $_.Name -eq 'app0' }).Offset | Should -Be 0x20000
        ($entries | Where-Object { $_.Name -eq 'app1' }).Offset | Should -Be 0x320000
        ($entries | Where-Object { $_.Name -eq 'littlefs' }).Offset | Should -Be 0x620000
    }
    It 'default_8mb table sha is not allow-listed' {
        $bytes = [System.IO.File]::ReadAllBytes((Join-Path $fixtures 'partitions_default_8mb.bin'))
        { Test-TableAllowed -TableBytes $bytes -AllowedSha256 @('8b3183d34d01892267fffd06e5a825c2d825d179551cf7b85db4e08223253dc1') } | Should -Throw
    }
}

Describe 'Partition regeneration' {
    It 'regenerates csv table to committed bytes' {
        $ref = [System.IO.File]::ReadAllBytes((Join-Path $fixtures 'partitions_8mb_ota.bin'))
        $gen = & python3 (Join-Path $fixtures 'gen_partition_table.py')
        $genBytes = [System.IO.File]::ReadAllBytes((Join-Path $fixtures 'partitions_8mb_ota.bin'))
        $genBytes.Length | Should -Be 3072
        $genBytes | Should -Be $ref
    }
}

Describe 'Partition table' {
    It 'parses default_8mb style app0 at 0x10000' {
        $bytes = [System.IO.File]::ReadAllBytes((Join-Path $fixtures 'partitions_default_8mb.bin'))
        $entries = @(Read-PartitionTable -TableBytes $bytes)
        $app0 = @($entries | Where-Object { $_.Name -eq 'app0' })
        $app0.Count | Should -Be 1
        $app0[0].Offset | Should -Be 0x10000
    }
    It 'parses partitions_8mb_ota entries' {
        $bytes = [System.IO.File]::ReadAllBytes((Join-Path $fixtures 'partitions_8mb_ota.bin'))
        $entries = Read-PartitionTable -TableBytes $bytes
        $app0 = $entries | Where-Object { $_.Name -eq 'app0' }
        $app0.Offset | Should -Be 0x20000
        $app0.Size | Should -Be 0x300000
    }
    It 'matches allowed sha256' {
        $bytes = [System.IO.File]::ReadAllBytes((Join-Path $fixtures 'partitions_8mb_ota.bin'))
        $r = Test-TableAllowed -TableBytes $bytes -AllowedSha256 @('8b3183d34d01892267fffd06e5a825c2d825d179551cf7b85db4e08223253dc1')
        $r.Allowed | Should -Be $true
    }
    It 'rejects unknown table sha' {
        $flash = [System.IO.File]::ReadAllBytes((Join-Path $fixtures 'flash_unknown_table.bin'))
        $table = $flash[0x8000..(0x8000 + 0xC00 - 1)]
        { Test-TableAllowed -TableBytes $table -AllowedSha256 @('8b3183d34d01892267fffd06e5a825c2d825d179551cf7b85db4e08223253dc1') } | Should -Throw
    }
}

Describe 'Otadata' {
    It 'selects slot for odd seq' {
        $secs = LoadOtadataSectors 'flash_ota_odd.bin'
        $table = Read-PartitionTable -TableBytes ([System.IO.File]::ReadAllBytes((Join-Path $fixtures 'partitions_8mb_ota.bin')))
        $slot = Select-AppSlot -PartitionEntries $table -OtadataSectors $secs
        $slot.SlotName | Should -Be 'app0'
    }
    It 'selects slot for even seq' {
        $secs = LoadOtadataSectors 'flash_ota_even.bin'
        $table = Read-PartitionTable -TableBytes ([System.IO.File]::ReadAllBytes((Join-Path $fixtures 'partitions_8mb_ota.bin')))
        $slot = Select-AppSlot -PartitionEntries $table -OtadataSectors $secs
        $slot.SlotName | Should -Be 'app1'
    }
    It 'STOP on NEW state' {
        $secs = LoadOtadataSectors 'flash_ota_new_stop.bin'
        $table = Read-PartitionTable -TableBytes ([System.IO.File]::ReadAllBytes((Join-Path $fixtures 'partitions_8mb_ota.bin')))
        { Select-AppSlot -PartitionEntries $table -OtadataSectors $secs } | Should -Throw '*NEW*'
    }
    It 'treats bad CRC as invalid' {
        $secs = LoadOtadataSectors 'flash_ota_bad_crc.bin'
        $secs[0].Valid | Should -Be $false
    }
    It 'STOP on PENDING_VERIFY' {
        $secs = LoadOtadataSectors 'flash_ota_pending_stop.bin'
        $table = Read-PartitionTable -TableBytes ([System.IO.File]::ReadAllBytes((Join-Path $fixtures 'partitions_8mb_ota.bin')))
        { Select-AppSlot -PartitionEntries $table -OtadataSectors $secs } | Should -Throw '*PENDING_VERIFY*'
    }
    It 'skips INVALID and uses other sector' {
        $secs = LoadOtadataSectors 'flash_ota_invalid_skip.bin'
        $table = Read-PartitionTable -TableBytes ([System.IO.File]::ReadAllBytes((Join-Path $fixtures 'partitions_8mb_ota.bin')))
        $slot = Select-AppSlot -PartitionEntries $table -OtadataSectors $secs
        $slot.SlotName | Should -Be 'app0'
        $slot.OtaSeq | Should -Be 5
    }
    It 'selects app0 on default_8mb synthetic flash' {
        $flash = [System.IO.File]::ReadAllBytes((Join-Path $fixtures 'flash_default_8mb.bin'))
        $table = Read-PartitionTable -TableBytes $flash[0x8000..(0x8000 + 0xC00 - 1)]
        $ota = $table | Where-Object { $_.Name -eq 'otadata' } | Select-Object -First 1
        $secs = Read-Otadata -OtadataBytes $flash[$ota.Offset..($ota.Offset + $ota.Size - 1)]
        $slot = Select-AppSlot -PartitionEntries $table -OtadataSectors $secs
        $slot.Offset | Should -Be 0x10000
    }
    It 'highest seq wins' {
        $secs = LoadOtadataSectors 'flash_ota_highest.bin'
        $table = Read-PartitionTable -TableBytes ([System.IO.File]::ReadAllBytes((Join-Path $fixtures 'partitions_8mb_ota.bin')))
        $slot = Select-AppSlot -PartitionEntries $table -OtadataSectors $secs
        $slot.OtaSeq | Should -Be 9
        $slot.SlotName | Should -Be 'app0'
    }
}

Describe 'Image header' {
    It 'accepts synthetic rust header' {
        $img = [System.IO.File]::ReadAllBytes((Join-Path $fixtures 'synthetic_firmware_header.bin'))
        $r = Test-ImageHeader -ImageBytes $img -SlotSize 0x300000
        $r.Ok | Should -Be $true
    }
    It 'rejects wrong chip id' {
        $img = [System.IO.File]::ReadAllBytes((Join-Path $fixtures 'synthetic_firmware_header.bin'))
        $img[12] = 0x05
        $r = Test-ImageHeader -ImageBytes $img
        $r.Ok | Should -Be $false
    }
    It 'rejects ELF' {
        $elf = [byte[]](0x7F, 0x45, 0x4C, 0x46) + ([byte[]](0) * 20)
        $r = Test-ImageHeader -ImageBytes $elf
        $r.Ok | Should -Be $false
    }
}

Describe 'Guards' {
    It 'rejects overlap with otadata' {
        $table = Read-PartitionTable -TableBytes ([System.IO.File]::ReadAllBytes((Join-Path $fixtures 'partitions_8mb_ota.bin')))
        { Test-WriteRangeAllowed -Offset 0xF000 -Length 0x100 -PartitionEntries $table } | Should -Throw
    }
    It 'rejects overlap with NVS' {
        $table = Read-PartitionTable -TableBytes ([System.IO.File]::ReadAllBytes((Join-Path $fixtures 'partitions_8mb_ota.bin')))
        { Test-WriteRangeAllowed -Offset 0x9000 -Length 0x100 -PartitionEntries $table } | Should -Throw
    }
    It 'rejects misaligned app offset' {
        $table = Read-PartitionTable -TableBytes ([System.IO.File]::ReadAllBytes((Join-Path $fixtures 'partitions_8mb_ota.bin')))
        { Test-FlashPlanConstraints -AppOffset 0x20010 -ImageLength 0x1000 -SlotSize 0x300000 -PartitionEntries $table } | Should -Throw
    }
}

Describe 'Test-OutsideSlotUnchanged' {
    It 'fails when byte outside slot flips' {
        $before = [System.IO.File]::ReadAllBytes((Join-Path $fixtures 'flash_ota_odd.bin'))
        $after = [byte[]]$before.Clone()
        $after[0x1000] = $after[0x1000] -bxor 0x01
        { Test-OutsideSlotUnchanged -BeforeBytes $before -AfterBytes $after -SlotOffset 0x20000 -ImageLength 0x1000 -SlotSize 0x300000 } | Should -Throw
    }
    It 'fails when padding inside slot flips' {
        $before = [System.IO.File]::ReadAllBytes((Join-Path $fixtures 'flash_ota_odd.bin'))
        $after = [byte[]]$before.Clone()
        $after[0x20000 + 0x1100] = $after[0x20000 + 0x1100] -bxor 0x01
        { Test-OutsideSlotUnchanged -BeforeBytes $before -AfterBytes $after -SlotOffset 0x20000 -ImageLength 0x1000 -SlotSize 0x300000 } | Should -Throw '*padding*'
    }
    It 'allows image prefix flip at slot boundary' {
        $before = [System.IO.File]::ReadAllBytes((Join-Path $fixtures 'flash_ota_odd.bin'))
        $after = [byte[]]$before.Clone()
        $after[0x20000] = $after[0x20000] -bxor 0x01
        { Test-OutsideSlotUnchanged -BeforeBytes $before -AfterBytes $after -SlotOffset 0x20000 -ImageLength 0x1000 -SlotSize 0x300000 } | Should -Throw -Not
    }
}

Describe 'Typed confirmation' {
    It 'accepts exact WRITE line' {
        Test-TypedConfirmation -UserInput 'WRITE 0x20000 9C1D2C5A' -ExpectedOffsetHex '0x20000' -ImageSha256Hex '9c1d2c5aab60ae87ac51ccdbd18784cf6380d9a416de39d175359eec33845d67' | Should -Be $true
    }
    It 'rejects lowercase sha8' {
        Test-TypedConfirmation -UserInput 'WRITE 0x20000 9c1d2c5a' -ExpectedOffsetHex '0x20000' -ImageSha256Hex '9c1d2c5aab60ae87ac51ccdbd18784cf6380d9a416de39d175359eec33845d67' | Should -Be $false
    }
}

Describe 'Connection and identity (pure)' {
    It 'returns connect attempt ladder' {
        (Get-ConnectAttempts).Count | Should -Be 3
        (Get-ConnectAttempts)[0].BeforeArg | Should -Be 'usb-reset'
    }
    It 'parses board-info MAC and chip' {
        $t = Get-Content -Raw (Join-Path $fixtures 'board_info_pass.txt')
        $id = Parse-BoardInfoIdentity -Text $t
        $id.Mac | Should -Be 'aa:bb:cc:dd:ee:01'
        $id.Chip | Should -Be 'esp32s3'
    }
    It 'passes CJ-shaped board-info security' {
        $t = Get-Content -Raw (Join-Path $fixtures 'board_info_pass.txt')
        { Test-BoardInfoSecurityAllowed -Text $t } | Should -Throw -Not
    }
    It 'passes CJ-shaped espefuse summary fixture' {
        $t = Get-Content -Raw (Join-Path $fixtures 'efuse_summary_pass.txt')
        (Parse-EfuseSecuritySummary -SummaryText $t).Allowed | Should -Be $true
    }
    It 'rejects MAC binding mismatch' {
        { Test-DeviceMacBinding -ObservedMac 'aa:bb:cc:dd:ee:02' -BoundMac 'aa:bb:cc:dd:ee:01' } | Should -Throw '*MAC*'
    }
    It 'download mode text mentions green LED' {
        (Get-DownloadModeInstructionText) | Should -Match 'green LED'
    }
}

Describe 'eFuse parser' {
    It 'detects secure boot enabled' {
        $t = @"
SECURE_BOOT_EN          : True
SPI_BOOT_CRYPT_CNT      : 0x0
"@
        { Parse-EfuseSecuritySummary -SummaryText $t } | Should -Throw
    }
    It 'passes when disabled' {
        $t = @"
SECURE_BOOT_EN          : False
SPI_BOOT_CRYPT_CNT      : 0x0
"@
        (Parse-EfuseSecuritySummary -SummaryText $t).Allowed | Should -Be $true
    }
}

Describe 'Get-MonitorVerdict' {
    BeforeAll {
        $script:S8MonitorBoot = 'stockbot-scanner-esp boot (slice S7); target=sticks3_u173; marker=stockbot-fw-target:sticks3_u173'
        $script:S8MonitorIdle = 'stockbot-scanner-esp idle (target=sticks3_u173)'
    }
    It 'passes good captures' {
        $c1 = ($script:S8MonitorBoot + "`n" + ($script:S8MonitorIdle + "`n") * 5)
        $c2 = $script:S8MonitorBoot + "`n"
        (Get-MonitorVerdict -Capture1Text $c1 -Capture2Text $c2 -TargetId 'sticks3_u173').Pass | Should -Be $true
    }
    It 'fails with no marker' {
        (Get-MonitorVerdict -Capture1Text 'noise' -Capture2Text 'noise' -TargetId 'sticks3_u173').Pass | Should -Be $false
    }
    It 'fails wrong target' {
        $c1 = 'stockbot-scanner-esp boot (slice S7); target=other'
        (Get-MonitorVerdict -Capture1Text $c1 -Capture2Text $c1 -TargetId 'sticks3_u173').Pass | Should -Be $false
    }
    It 'fails reboot loop in capture1' {
        $c1 = ($script:S8MonitorBoot + "`n" + $script:S8MonitorBoot + "`n" + ($script:S8MonitorIdle + "`n") * 5)
        $c2 = $script:S8MonitorBoot
        (Get-MonitorVerdict -Capture1Text $c1 -Capture2Text $c2 -TargetId 'sticks3_u173').Pass | Should -Be $false
    }
    It 'ignores garbage lines' {
        $c1 = ("garbage`n" + $script:S8MonitorBoot + "`n" + ($script:S8MonitorIdle + "`n") * 4)
        $c2 = $script:S8MonitorBoot
        (Get-MonitorVerdict -Capture1Text $c1 -Capture2Text $c2 -TargetId 'sticks3_u173').Pass | Should -Be $true
    }
    It 'fails empty capture' {
        (Get-MonitorVerdict -Capture1Text '' -Capture2Text '' -TargetId 'sticks3_u173').Pass | Should -Be $false
    }
    It 'fails idle count boundary 3' {
        $c1 = ($script:S8MonitorBoot + "`n" + ($script:S8MonitorIdle + "`n") * 3)
        $c2 = $script:S8MonitorBoot
        (Get-MonitorVerdict -Capture1Text $c1 -Capture2Text $c2 -TargetId 'sticks3_u173').Pass | Should -Be $false
    }
}

Describe 'PS 5.1 syntax guard' {
    It 'module and cli avoid PS7-only operators' {
        $root = Split-Path -Parent $PSScriptRoot
        foreach ($name in @('StockbotFlash.psm1', 'StockbotFlash.Cli.ps1', 'Invoke-StockbotFlash.ps1')) {
            $path = if ($env:STOCKBOT_MODULE_PATH -and $name -eq 'StockbotFlash.psm1') { $env:STOCKBOT_MODULE_PATH } else { Join-Path $root $name }
            $raw = Get-Content -Raw -LiteralPath $path
            ($raw -match '\?\?') | Should -Be $false
            ($raw -match '\?\.') | Should -Be $false
        }
    }
}