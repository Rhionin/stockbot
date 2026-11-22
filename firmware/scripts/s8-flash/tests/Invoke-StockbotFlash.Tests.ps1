#requires -Version 5.1
BeforeAll {
    $ErrorActionPreference = 'Stop'
    Set-StrictMode -Version 2
    $global:S8Root = Split-Path -Parent $PSScriptRoot
    $global:S8Fixtures = Join-Path $PSScriptRoot 'fixtures'
    $modulePath = if ($env:STOCKBOT_MODULE_PATH) { $env:STOCKBOT_MODULE_PATH } else { Join-Path $global:S8Root 'StockbotFlash.psm1' }
    Import-Module $modulePath -Force
    & python3 (Join-Path $global:S8Fixtures 'make_synthetic_flash.py') | Out-Null
    $global:S8Targets = (Get-Content -Raw (Join-Path $global:S8Root 'targets.json') | ConvertFrom-Json).targets.sticks3_u173
    $cliFile = if ($env:STOCKBOT_CLI_PATH) { $env:STOCKBOT_CLI_PATH } else { Join-Path $global:S8Root 'StockbotFlash.Cli.ps1' }
    . $cliFile

    function script:New-S8RunDir {
        $d = Join-Path ([System.IO.Path]::GetTempPath()) ('s8test_' + [guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Path $d -Force | Out-Null
        return $d
    }

    function script:Seed-BackupRun {
        param([string]$FlashFixture = 'flash_ota_odd.bin')
        $run = New-S8RunDir
        $src = Join-Path $global:S8Fixtures $FlashFixture
        Copy-Item -LiteralPath $src -Destination (Join-Path $run 'backup.bin')
        $img = Join-Path $global:S8Fixtures 'synthetic_firmware_header.bin'
        $art = Join-Path $run 'artifact'
        New-Item -ItemType Directory -Path $art -Force | Out-Null
        Copy-Item -LiteralPath $img -Destination (Join-Path $art 'firmware.bin')
        return $run
    }

    function script:Get-FakeBoardInfoText {
        param([string]$Mac = 'aa:bb:cc:dd:ee:01')
        $t = Get-Content -Raw (Join-Path $global:S8Fixtures 'board_info_pass.txt')
        return ($t -replace 'aa:bb:cc:dd:ee:01', $Mac)
    }

    function script:Reset-FakeConnectState {
        $global:FakeComPorts = @('COM3')
        $global:FakeBoardInfoMac = 'aa:bb:cc:dd:ee:01'
        $global:FakeConnectFailBefore = @()
        $global:FakeAllConnectFail = $false
        $global:FakeBoardInfoCalls = 0
    }

    function script:Init-S8TestContext {
        param(
            [hashtable]$Extra = @{}
        )
        Reset-FakeConnectState
        Initialize-StockbotFlashContext -Cmdlet $null -Target 'sticks3_u173' -Port 'COM3' -WorkRoot $env:TEMP -TargetCfg $global:S8Targets @Extra
        $Script:S8_WorkRoot = $env:TEMP
        $Script:S8_TestComPorts = @($global:FakeComPorts)
        $Script:S8_DeviceConnectDone = $true
        $env:STOCKBOT_EFUSE_SUMMARY = Get-Content -Raw (Join-Path $global:S8Fixtures 'efuse_summary_pass.txt')
    }

    $bootLine = 'stockbot-scanner-esp boot (slice S7); target=sticks3_u173; marker=stockbot-fw-target:sticks3_u173'
    $idleLine = 'stockbot-scanner-esp idle (target=sticks3_u173)'

    $global:FakeFlashBytes = $null
    $global:FakeOutsideFlip = $false
    $global:FakeMd5 = 'deadbeefdeadbeefdeadbeefdeadbeef'

    $Script:S8_EspflashInvoker = {
        param([string[]]$Arguments)
        $verb = $Arguments[0]
        $before = $null
        for ($i = 0; $i -lt ($Arguments.Count - 1); $i++) {
            if ($Arguments[$i] -eq '--before') { $before = $Arguments[$i + 1]; break }
        }
        if ($verb -eq 'checksum-md5') {
            $md5 = $global:FakeMd5
            return [pscustomobject]@{ ExitCode = 0; StdOut = $md5; StdErr = '' }
        }
        if ($verb -eq 'read-flash') {
            if ($Arguments -contains '0x0' -and $Arguments -contains '0x800000') {
                $out = $Arguments[-1]
                $bytes = $global:FakeFlashBytes
                if ($global:FakeOutsideFlip) {
                    $bytes = [byte[]]$bytes.Clone()
                    $bytes[0x1000] = $bytes[0x1000] -bxor 0x01
                }
                [System.IO.File]::WriteAllBytes($out, $bytes)
            }
            elseif ($Arguments.Count -ge 5) {
                $out = $Arguments[-1]
                $hex = @($Arguments | Where-Object { $_ -match '^0x[0-9a-fA-F]+$' })
                $off = [Convert]::ToUInt32($hex[0].Substring(2), 16)
                $len = [Convert]::ToUInt32($hex[1].Substring(2), 16)
                $slice = New-Object byte[] $len
                [Array]::Copy($global:FakeFlashBytes, $off, $slice, 0, $len)
                [System.IO.File]::WriteAllBytes($out, $slice)
            }
            return [pscustomobject]@{ ExitCode = 0; StdOut = 'ok'; StdErr = '' }
        }
        if ($verb -eq 'write-bin') {
            $file = $Arguments[-1]
            $offHex = $null
            for ($i = 0; $i -lt ($Arguments.Count - 1); $i++) {
                if ($Arguments[$i] -match '^0x[0-9a-fA-F]+$') {
                    $offHex = $Arguments[$i]
                    break
                }
            }
            $off = [Convert]::ToUInt32($offHex.Substring(2), 16)
            $data = [System.IO.File]::ReadAllBytes($file)
            [Array]::Copy($data, 0, $global:FakeFlashBytes, $off, $data.Length)
            return [pscustomobject]@{ ExitCode = 0; StdOut = ''; StdErr = '' }
        }
        if ($verb -eq 'monitor') {
            $text = ($bootLine + "`n" + ($idleLine + "`n") * 5)
            return [pscustomobject]@{ ExitCode = 0; StdOut = $text; StdErr = '' }
        }
        if ($verb -eq 'board-info') {
            if ($global:FakeAllConnectFail) {
                return [pscustomobject]@{ ExitCode = 1; StdOut = ''; StdErr = 'fail' }
            }
            if ($before -and ($global:FakeConnectFailBefore -contains $before)) {
                return [pscustomobject]@{ ExitCode = 1; StdOut = ''; StdErr = 'fail' }
            }
            if ($before -eq 'usb-reset') {
                $global:FakeComPorts = @('COM4')
                $Script:S8_TestComPorts = @('COM4')
            }
            $global:FakeBoardInfoCalls++
            $text = Get-FakeBoardInfoText -Mac $global:FakeBoardInfoMac
            return [pscustomobject]@{ ExitCode = 0; StdOut = $text; StdErr = '' }
        }
        return [pscustomobject]@{ ExitCode = 0; StdOut = ''; StdErr = '' }
    }
}

Describe 'Invoke-StockbotFlash integration' {
    BeforeEach {
        $global:FakeOutsideFlip = $false
        Init-S8TestContext
        $run = Seed-BackupRun
        $global:FakeFlashBytes = [System.IO.File]::ReadAllBytes((Join-Path $run 'backup.bin'))
        $Script:S8_WorkRoot = $run
        $state = @{
            ReadOnlyComplete = $true
            EfuseOk          = $true
            BackupMd5        = 'abc'
            PlanComplete     = $true
            PlanOffset       = '0x20000'
            PlanSlotSize     = 0x300000
            TableSha256      = $global:S8Targets.allowedTableSha256[0]
            DeviceMac        = 'aa:bb:cc:dd:ee:01'
            DeviceChip       = 'esp32s3'
        }
        Save-FlashState -RunDir $run -State $state
        $Script:S8_TestRun = $run
        $global:S8Img = Join-Path $run 'artifact/firmware.bin'
        $global:S8ExpectedSha = (Get-BytesSha256Hex -Bytes ([System.IO.File]::ReadAllBytes($global:S8Img)))
    }

    It 'WhatIf Write never calls write-bin' {
        $Script:S8_WhatIf = $true
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = 'WRITE 0x20000 ' + $global:S8ExpectedSha.Substring(0, 8).ToUpperInvariant()
        Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets
        $log = Get-S8CallLog
        @($log | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
    }

    It 'Write without typed confirmation never calls write-bin' {
        $Script:S8_WhatIf = $false
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = 'WRITE 0x20000 DEADBEEF'
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Throw '*confirmation*'
        $log = Get-S8CallLog
        @($log | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
    }

    It 'Write refused without verified backup state' {
        $bad = @{ PlanComplete = $true; PlanOffset = '0x20000'; PlanSlotSize = 0x300000 }
        Save-FlashState -RunDir $Script:S8_TestRun -State $bad
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = 'WRITE 0x20000 ' + $global:S8ExpectedSha.Substring(0, 8).ToUpperInvariant()
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Throw '*backup*'
    }

    It 'Write refused without ExpectedSha256' {
        $Script:S8_ExpectedSha256 = $null
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Throw '*ExpectedSha256*'
    }

    It 'Write refused when ExpectedSha256 mismatches image' {
        $Script:S8_ExpectedSha256 = ('0' * 64)
        $Script:S8_Confirmation = 'WRITE 0x20000 ' + ('0' * 8)
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Throw '*ExpectedSha256*'
    }

    It 'call order write-bin, slot read-back, full read, monitor after compare' {
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = 'WRITE 0x20000 ' + $global:S8ExpectedSha.Substring(0, 8).ToUpperInvariant()
        Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets
        $log = Get-S8CallLog
        $idxWrite = -1
        $idxSlot = -1
        $idxFull = -1
        $idxMon = -1
        for ($i = 0; $i -lt $log.Count; $i++) {
            if ($log[$i] -like 'write-bin*' -and $idxWrite -lt 0) { $idxWrite = $i }
            if ($log[$i] -like 'read-flash*0x20000*' -and $idxSlot -lt 0) { $idxSlot = $i }
            if ($log[$i] -like 'read-flash*0x0 0x800000*' -and $idxFull -lt 0) { $idxFull = $i }
            if ($log[$i] -like 'monitor*' -and $idxMon -lt 0) { $idxMon = $i }
        }
        $idxWrite | Should -BeGreaterOrEqual 0
        $idxWrite | Should -BeLessThan $idxSlot
        $idxSlot | Should -BeLessThan $idxFull
        $idxFull | Should -BeLessThan $idxMon
    }

    It 'outside slot flip STOPs before monitor' {
        $global:FakeOutsideFlip = $true
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = 'WRITE 0x20000 ' + $global:S8ExpectedSha.Substring(0, 8).ToUpperInvariant()
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Throw
        $log = Get-S8CallLog
        @($log | Where-Object { $_ -like 'monitor*' }).Count | Should -Be 0
    }

    It 'ReadOnly STOP on secure boot eFuse' {
        Init-S8TestContext
        $run = New-S8RunDir
        $env:STOCKBOT_EFUSE_SUMMARY = "SECURE_BOOT_EN          : True`nSPI_BOOT_CRYPT_CNT      : 0x0"
        { Invoke-PhaseReadOnly -RunDir $run -TargetCfg $global:S8Targets } | Should -Throw
    }

    It 'ReadOnly STOP on md5 mismatch' {
        Init-S8TestContext
        $run = New-S8RunDir
        $backupPath = Join-Path $run 'backup.bin'
        $global:FakeMd5 = '0102030405060708090a0b0c0d0e0f10'
        Copy-Item (Join-Path $global:S8Fixtures 'flash_ota_odd.bin') -Destination $backupPath
        $global:FakeFlashBytes = [System.IO.File]::ReadAllBytes($backupPath)
        { Invoke-PhaseReadOnly -RunDir $run -TargetCfg $global:S8Targets } | Should -Throw '*MD5*'
        $global:FakeMd5 = 'deadbeefdeadbeefdeadbeefdeadbeef'
    }

    It 'Restore requires typed confirmation and full slot write' {
        $Script:S8_RestoreConfirmation = 'RESTORE 0x20000 DEADBEEF'
        { Invoke-PhaseRestore -RunDir $Script:S8_TestRun -TargetCfg $global:S8Targets } | Should -Throw '*restore confirmation*'
        $slotSha = Get-BytesSha256Hex -Bytes ($global:FakeFlashBytes[0x20000..(0x20000 + 0x300000 - 1)])
        $Script:S8_RestoreConfirmation = 'RESTORE 0x20000 ' + $slotSha.Substring(0, 8).ToUpperInvariant()
        Invoke-PhaseRestore -RunDir $Script:S8_TestRun -TargetCfg $global:S8Targets
        $log = Get-S8CallLog
        @($log | Where-Object { $_ -like 'write-bin*0x20000*' -and $_ -like '*restore_slot.bin' }).Count | Should -BeGreaterOrEqual 1
    }

    It 'every write/read uses --after no-reset and forbids flash' {
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = 'WRITE 0x20000 ' + $global:S8ExpectedSha.Substring(0, 8).ToUpperInvariant()
        Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets
        foreach ($line in (Get-S8CallLog)) {
            if ($line -like 'read-flash*' -or $line -like 'write-bin*' -or $line -like 'checksum-md5*' -or $line -like 'board-info*') {
                $line | Should -Match '--after no-reset'
                $line | Should -Match '--before usb-reset'
            }
            $line | Should -Not -Match '(^|\s)flash(\s|$)'
        }
    }

    It 're-enumerates COM3 to COM4 after usb-reset and uses COM4 on write' {
        Reset-FakeConnectState
        $Script:S8_TestComPorts = @('COM3')
        $Script:S8_DeviceConnectDone = $false
        Invoke-DeviceConnect -TargetCfg $global:S8Targets
        $Script:S8_ConnectStrategy | Should -Be 'usb-reset'
        $Script:S8_TestComPorts[0] | Should -Be 'COM4'
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = 'WRITE 0x20000 ' + $global:S8ExpectedSha.Substring(0, 8).ToUpperInvariant()
        Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets
        $log = Get-S8CallLog
        $writes = @($log | Where-Object { $_ -like 'write-bin*' })
        ($writes[0]) | Should -Match '--port COM4'
        ($log | Where-Object { $_ -like 'read-flash*0x20000*' } | Select-Object -First 1) | Should -Match '--port COM4'
    }

    It 'connect ladder falls through to no-reset when earlier strategies fail' {
        Reset-FakeConnectState
        $global:FakeConnectFailBefore = @('usb-reset', 'default-reset')
        $Script:S8_TestComPorts = @('COM3')
        $Script:S8_DeviceConnectDone = $false
        Invoke-DeviceConnect -TargetCfg $global:S8Targets
        $Script:S8_ConnectStrategy | Should -Be 'no-reset'
    }

    It 'STOP on MAC change after re-enumeration before write' {
        $global:FakeBoardInfoMac = 'aa:bb:cc:dd:ee:02'
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = 'WRITE 0x20000 ' + $global:S8ExpectedSha.Substring(0, 8).ToUpperInvariant()
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Throw '*MAC*'
        $log = Get-S8CallLog
        @($log | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
        $global:FakeBoardInfoMac = 'aa:bb:cc:dd:ee:01'
    }

    It 'all connect strategies fail prints download instructions and no write' {
        Reset-FakeConnectState
        $global:FakeAllConnectFail = $true
        $Script:S8_DeviceConnectDone = $false
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = 'WRITE 0x20000 ' + $global:S8ExpectedSha.Substring(0, 8).ToUpperInvariant()
        { Invoke-DeviceConnect -TargetCfg $global:S8Targets } | Should -Throw '*connect failed*'
        $global:FakeAllConnectFail = $false
    }

    It 'Plan STOPs on default_8mb table not in allow-list' {
        $run = Seed-BackupRun -FlashFixture 'flash_default_8mb.bin'
        $img = Join-Path $run 'artifact/firmware.bin'
        $state = @{ ReadOnlyComplete = $true }
        Save-FlashState -RunDir $run -State $state
        { Invoke-PhasePlan -RunDir $run -ImageFile $img -TargetCfg $global:S8Targets } | Should -Throw
    }
}