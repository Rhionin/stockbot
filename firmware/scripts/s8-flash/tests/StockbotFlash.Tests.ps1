#requires -Version 5.1
BeforeAll {
    $ErrorActionPreference = 'Stop'
    Set-StrictMode -Version 2
    $root = Split-Path -Parent $PSScriptRoot
    $fixtures = Join-Path $PSScriptRoot 'fixtures'
    $tempRoot = if ($env:TEMP) { $env:TEMP } elseif ($env:TMPDIR) { $env:TMPDIR } else { '/tmp' }
    $genDir = Join-Path $tempRoot ('s8_fixture_gen_' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $genDir -Force | Out-Null
    Push-Location $genDir
  try {
        & python3 (Join-Path $fixtures 'gen_partition_table.py') | Out-Null
        & python3 (Join-Path $fixtures 'make_synthetic_flash.py') | Out-Null
    }
    finally {
        Pop-Location
        Remove-Item -LiteralPath $genDir -Recurse -Force -ErrorAction SilentlyContinue
    }
    $Script:S8_AllowEnvTestSeams = $true
    if ($env:STOCKBOT_MODULE_PATH -and -not $Script:S8_AllowEnvTestSeams) {
        throw 'STOP: STOCKBOT_MODULE_PATH is set but test seams are disabled'
    }
    $modulePath = if ($env:STOCKBOT_MODULE_PATH) { $env:STOCKBOT_MODULE_PATH } else { Join-Path $root 'StockbotFlash.psm1' }
    Import-Module $modulePath -Force
    function script:LoadOtadataSectors([string]$file) {
        $flash = [System.IO.File]::ReadAllBytes((Join-Path $fixtures $file))
        $table = Read-PartitionTable -TableBytes $flash[0x8000..(0x8000 + 0xC00 - 1)]
        $ota = $table | Where-Object { $_.Name -eq 'otadata' } | Select-Object -First 1
        $blob = $flash[$ota.Offset..($ota.Offset + $ota.Size - 1)]
        return Read-Otadata -OtadataBytes $blob
    }
}

Describe 'Table allow-list provenance' {
    It 'csv compile matches the allow-listed sha256' {
        $csvBin = [System.IO.File]::ReadAllBytes((Join-Path $fixtures 'partitions_8mb_ota.bin'))
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
        Push-Location $fixtures
        try {
            $stdout = (& python3 (Join-Path $fixtures 'gen_partition_table.py') | Out-String).Trim()
        }
        finally {
            Pop-Location
        }
        $stdout | Should -Be (Get-BytesSha256Hex -Bytes $ref)
        ([System.IO.File]::ReadAllBytes((Join-Path $fixtures 'partitions_8mb_ota.bin'))) | Should -Be $ref
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
    It 'CRC matches ESP-IDF esp_rom_crc32_le known vectors' {
        (Get-OtadataEntryCrc -EntryBytes ([BitConverter]::GetBytes([uint32]1))) | Should -Be ([uint32]0x4743989A)
        (Get-OtadataEntryCrc -EntryBytes ([BitConverter]::GetBytes([uint32]2))) | Should -Be ([uint32]0x55F63774)
    }
    It 'skips ABORTED and uses other sector' {
        $secs = LoadOtadataSectors 'flash_ota_aborted_skip.bin'
        $table = Read-PartitionTable -TableBytes ([System.IO.File]::ReadAllBytes((Join-Path $fixtures 'partitions_8mb_ota.bin')))
        $slot = Select-AppSlot -PartitionEntries $table -OtadataSectors $secs
        $slot.OtaSeq | Should -Be 5
        $slot.SlotName | Should -Be 'app0'
    }
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
    It 'rejects write into low flash below 0x9000' {
        $table = Read-PartitionTable -TableBytes ([System.IO.File]::ReadAllBytes((Join-Path $fixtures 'partitions_8mb_ota.bin')))
        { Test-WriteRangeAllowed -Offset 0x8000 -Length 0x100 -PartitionEntries $table } | Should -Throw '*protected low flash*'
    }
    It 'rejects range that starts before a protected partition and ends inside it' {
        $entries = @([pscustomobject]@{ Name = 'nvs'; Type = 1; Subtype = 2; Offset = 0x20000; Size = 0x1000; Flags = 0 })
        { Test-WriteRangeAllowed -Offset 0x1F000 -Length 0x2000 -PartitionEntries $entries } | Should -Throw '*nvs*'
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
    It 'accepts 0xFF in the erased tail of the last written sector' {
        $before = [System.IO.File]::ReadAllBytes((Join-Path $fixtures 'flash_ota_odd.bin'))
        for ($i = 0x20000; $i -lt 0x22000; $i++) { $before[$i] = 0x5A }
        $after = [byte[]]$before.Clone()
        for ($i = 0x20000 + 0x1800; $i -lt 0x22000; $i++) { $after[$i] = 0xFF }
        { Test-OutsideSlotUnchanged -BeforeBytes $before -AfterBytes $after -SlotOffset 0x20000 -ImageLength 0x1800 -SlotSize 0x300000 } | Should -Not -Throw
    }
    It 'STOPs when the erased tail holds a byte that is neither 0xFF nor the old byte' {
        $before = [System.IO.File]::ReadAllBytes((Join-Path $fixtures 'flash_ota_odd.bin'))
        for ($i = 0x20000; $i -lt 0x22000; $i++) { $before[$i] = 0x5A }
        $after = [byte[]]$before.Clone()
        $after[0x20000 + 0x1900] = 0x00
        { Test-OutsideSlotUnchanged -BeforeBytes $before -AfterBytes $after -SlotOffset 0x20000 -ImageLength 0x1800 -SlotSize 0x300000 } | Should -Throw '*erased tail*'
    }
    It 'STOPs when 0xFF appears past the last written sector' {
        $before = [System.IO.File]::ReadAllBytes((Join-Path $fixtures 'flash_ota_odd.bin'))
        for ($i = 0x20000; $i -lt 0x22000; $i++) { $before[$i] = 0x5A }
        $after = [byte[]]$before.Clone()
        $after[0x20000 + 0x2000 - 0x800] = 0xFF
        $after[0x21000] = 0xFF
        { Test-OutsideSlotUnchanged -BeforeBytes $before -AfterBytes $after -SlotOffset 0x20000 -ImageLength 0x800 -SlotSize 0x300000 } | Should -Throw '*padding*'
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

Describe 'Board-info optional security lines' {
    It 'board-info security fails closed on an unrecognised token' {
        { Test-BoardInfoSecurityAllowed -Text "Secure Boot: unknown`nFlash Encryption: Disabled" } | Should -Throw '*fail closed*'
    }
    It 'board-info security STOPs when secure boot is enabled' {
        { Test-BoardInfoSecurityAllowed -Text (Get-Content -Raw (Join-Path $fixtures 'board_info_secureboot.txt')) } | Should -Throw '*board-info*'
    }
    It 'board-info security STOPs when flash encryption is enabled' {
        { Test-BoardInfoSecurityAllowed -Text (Get-Content -Raw (Join-Path $fixtures 'board_info_flashenc.txt')) } | Should -Throw '*board-info*'
    }
    It 'board-info with no security lines is allowed' {
        Test-BoardInfoSecurityAllowed -Text (Get-Content -Raw (Join-Path $fixtures 'board_info_pass.txt')) | Should -Be $true
    }
}

Describe 'Connection and identity (pure)' {
    It 'returns connect attempt ladder' {
        (Get-ConnectAttempts).Count | Should -Be 3
        (Get-ConnectAttempts)[0].BeforeArg | Should -Be 'usb-reset'
    }
    It 'parses espflash 3.3.0 board-info MAC, chip and flash size' {
        $t = Get-Content -Raw (Join-Path $fixtures 'board_info_pass.txt')
        $id = Parse-BoardInfoIdentity -Text $t
        $id.Mac | Should -Be 'aa:bb:cc:dd:ee:01'
        $id.Chip | Should -Be 'esp32s3'
        $id.FlashSize | Should -Be 8388608
    }
    It 'STOPs when board-info has no "MAC address:" line' {
        $t = "Chip type:         esp32s3 (revision v0.2)`nFlash size:        8MB`nMAC:               aa:bb:cc:dd:ee:01"
        { Parse-BoardInfoIdentity -Text $t } | Should -Throw '*MAC address*'
    }
    It 'STOPs when board-info has no chip type' {
        { Parse-BoardInfoIdentity -Text "MAC address:       aa:bb:cc:dd:ee:01" } | Should -Throw '*Chip type*'
    }
    It 'STOPs on wrong flash size' {
        $id = [pscustomobject]@{ Mac = 'aa:bb:cc:dd:ee:01'; Chip = 'esp32s3'; FlashSize = 4194304 }
        { Test-BoardIdentityMatchesTarget -Identity $id -Chip 'esp32s3' -FlashSize 8388608 } | Should -Throw '*flash size*'
    }
    It 'STOPs on wrong chip' {
        $id = [pscustomobject]@{ Mac = 'aa:bb:cc:dd:ee:01'; Chip = 'esp32c3'; FlashSize = 8388608 }
        { Test-BoardIdentityMatchesTarget -Identity $id -Chip 'esp32s3' -FlashSize 8388608 } | Should -Throw '*chip*'
    }
    It 'STOPs when flash size is missing' {
        $id = [pscustomobject]@{ Mac = 'aa:bb:cc:dd:ee:01'; Chip = 'esp32s3'; FlashSize = $null }
        { Test-BoardIdentityMatchesTarget -Identity $id -Chip 'esp32s3' -FlashSize 8388608 } | Should -Throw '*Flash size*'
    }
    It 'rejects MAC binding mismatch' {
        { Test-DeviceMacBinding -ObservedMac 'aa:bb:cc:dd:ee:02' -BoundMac 'aa:bb:cc:dd:ee:01' } | Should -Throw '*MAC*'
    }
    It 'rejects MAC that differs from -ExpectedMac' {
        { Test-DeviceMacBinding -ObservedMac 'aa:bb:cc:dd:ee:02' -ExpectedMac 'aa:bb:cc:dd:ee:01' } | Should -Throw '*ExpectedMac*'
    }
    It 'download mode text mentions green LED' {
        (Get-DownloadModeInstructionText) | Should -Match 'green LED'
    }
}

Describe 'eFuse parser' {
    BeforeAll {
        $script:EfuseFixture = Get-Content -Raw (Join-Path $fixtures 'espefuse_summary_virt_esp32s3.json')
        function script:Set-EfuseValue([string]$Field, $Value) {
            $doc = $script:EfuseFixture | ConvertFrom-Json
            $doc.$Field.value = $Value
            return ($doc | ConvertTo-Json -Depth 5)
        }
    }
    It 'passes the espefuse 4.8.1 json summary fixture and returns its MAC' {
        $r = Parse-EfuseSecuritySummary -SummaryJson $script:EfuseFixture
        $r.Allowed | Should -Be $true
        $r.Mac | Should -Be 'aa:bb:cc:dd:ee:01'
    }
    It 'STOPs when secure boot is enabled' {
        { Parse-EfuseSecuritySummary -SummaryJson (Set-EfuseValue 'SECURE_BOOT_EN' $true) } | Should -Throw '*secure boot is enabled*'
    }
    It 'STOPs when flash encryption is enabled' {
        { Parse-EfuseSecuritySummary -SummaryJson (Set-EfuseValue 'SPI_BOOT_CRYPT_CNT' 'Enable') } | Should -Throw '*flash encryption is enabled*'
    }
    It 'STOPs when SPI_BOOT_CRYPT_CNT is a nonzero count' {
        { Parse-EfuseSecuritySummary -SummaryJson (Set-EfuseValue 'SPI_BOOT_CRYPT_CNT' 2) } | Should -Throw '*flash encryption is enabled*'
    }
    It 'STOPs on an unrecognised SPI_BOOT_CRYPT_CNT value' {
        { Parse-EfuseSecuritySummary -SummaryJson (Set-EfuseValue 'SPI_BOOT_CRYPT_CNT' 'unknown') } | Should -Throw '*not Disable or Enable*'
    }
    It 'STOPs when SECURE_BOOT_EN is not a boolean' {
        { Parse-EfuseSecuritySummary -SummaryJson (Set-EfuseValue 'SECURE_BOOT_EN' 'unknown') } | Should -Throw '*not true or false*'
    }
    It 'STOPs when a security field is missing' {
        $doc = $script:EfuseFixture | ConvertFrom-Json
        $doc.PSObject.Properties.Remove('SECURE_BOOT_EN')
        { Parse-EfuseSecuritySummary -SummaryJson ($doc | ConvertTo-Json -Depth 5) } | Should -Throw '*no SECURE_BOOT_EN*'
    }
    It 'STOPs on text that is not JSON' {
        { Parse-EfuseSecuritySummary -SummaryJson 'SECURE_BOOT_EN : False' } | Should -Throw '*not valid JSON*'
    }
    It 'STOPs on empty summary' {
        { Parse-EfuseSecuritySummary -SummaryJson '' } | Should -Throw '*empty*'
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

Describe 'Safety gates (Otadata and write range)' {
    It 'skips ABORTED and uses other sector' {
        if (-not (Test-Path (Join-Path $fixtures 'flash_ota_aborted_skip.bin'))) {
            & python3 (Join-Path $fixtures 'make_synthetic_flash.py') | Out-Null
        }
        $flash = [System.IO.File]::ReadAllBytes((Join-Path $fixtures 'flash_ota_aborted_skip.bin'))
        $table = Read-PartitionTable -TableBytes $flash[0x8000..(0x8000 + 0xC00 - 1)]
        $ota = $table | Where-Object { $_.Name -eq 'otadata' } | Select-Object -First 1
        $secs = Read-Otadata -OtadataBytes $flash[$ota.Offset..($ota.Offset + $ota.Size - 1)]
        $slot = Select-AppSlot -PartitionEntries $table -OtadataSectors $secs
        $slot.OtaSeq | Should -Be 5
        $slot.SlotName | Should -Be 'app0'
    }
    It 'rejects writes into partition table and bootloader region' {
        $table = Read-PartitionTable -TableBytes ([System.IO.File]::ReadAllBytes((Join-Path $fixtures 'partitions_8mb_ota.bin')))
        { Test-WriteRangeAllowed -Offset 0x8000 -Length 0x100 -PartitionEntries $table } | Should -Throw '*0x9000*'
        { Test-WriteRangeAllowed -Offset 0x1000 -Length 0x100 -PartitionEntries $table } | Should -Throw '*0x9000*'
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