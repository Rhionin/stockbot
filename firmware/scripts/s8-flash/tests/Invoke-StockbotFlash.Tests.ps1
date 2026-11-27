#requires -Version 5.1
BeforeAll {
    $ErrorActionPreference = 'Stop'
    Set-StrictMode -Version 2
    $global:S8Root = Split-Path -Parent $PSScriptRoot
    $global:S8Fixtures = Join-Path $PSScriptRoot 'fixtures'
    $Script:S8_AllowEnvTestSeams = $true
    if ($env:STOCKBOT_MODULE_PATH -and -not $Script:S8_AllowEnvTestSeams) {
        throw 'STOP: STOCKBOT_MODULE_PATH is set but test seams are disabled'
    }
    $modulePath = if ($env:STOCKBOT_MODULE_PATH) { $env:STOCKBOT_MODULE_PATH } else { Join-Path $global:S8Root 'StockbotFlash.psm1' }
    Import-Module $modulePath -Force
    $tempRoot = if ($env:TEMP) { $env:TEMP } elseif ($env:TMPDIR) { $env:TMPDIR } else { '/tmp' }
    $genDir = Join-Path $tempRoot ('s8_fixture_gen_' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $genDir -Force | Out-Null
    Push-Location $genDir
    try {
        & python3 (Join-Path $global:S8Fixtures 'make_synthetic_flash.py') | Out-Null
        & python3 (Join-Path $global:S8Fixtures 'gen_partition_table.py') | Out-Null
    }
    finally {
        Pop-Location
        Remove-Item -LiteralPath $genDir -Recurse -Force -ErrorAction SilentlyContinue
    }
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
        $path = if ($global:FakeBoardInfoFixture) { $global:FakeBoardInfoFixture } else { (Join-Path $global:S8Fixtures 'board_info_pass.txt') }
        $t = Get-Content -Raw $path
        return ($t -replace 'aa:bb:cc:dd:ee:01', $Mac)
    }

    function script:Reset-FakeConnectState {
        $global:FakeComPorts = @('COM3')
        $global:FakeBoardInfoMac = 'aa:bb:cc:dd:ee:01'
        $global:FakeConnectFailBefore = @()
        $global:FakeAllConnectFail = $false
        $global:FakeBoardInfoCalls = 0
        $global:FakeReadWriteMac = $null
        $global:FakeWriteBinBoardInfo = $true
        $global:FakeSlotReadCorrupt = $false
    $global:FakeWriteCorrupt = $false
    $global:FakeBoardInfoFixture = $null
    $global:FakeMonitorBootLoop = $false
        $global:FakeEfuseJson = Get-Content -Raw (Join-Path $global:S8Fixtures 'espefuse_summary_virt_esp32s3.json')
        $global:FakeEfuseExit = 0
    }

    function script:Init-S8TestContext {
        param(
            [hashtable]$Extra = @{}
        )
        Reset-FakeConnectState
        $Script:S8_AllowEnvTestSeams = $true
        $workRoot = if ($env:TEMP) { $env:TEMP } elseif ($env:TMPDIR) { $env:TMPDIR } else { '/tmp' }
        Initialize-StockbotFlashContext -Cmdlet $null -Target 'sticks3_u173' -Port 'COM3' -WorkRoot $workRoot -TargetCfg $global:S8Targets @Extra
        $Script:S8_WorkRoot = $workRoot
        $Script:S8_TestComPorts = @($global:FakeComPorts)
        $Script:S8_DeviceConnectDone = $true
    }

    function script:Set-FakeEfuseValue([string]$Field, $Value) {
        $doc = $global:FakeEfuseJson | ConvertFrom-Json
        $doc.$Field.value = $Value
        $global:FakeEfuseJson = ($doc | ConvertTo-Json -Depth 5)
    }

    function script:Get-WriteConfirmation {
        return ('WRITE 0x20000 ' + $global:S8ExpectedSha.Substring(0, 8).ToUpperInvariant())
    }

    $Script:S8_EspefuseInvoker = {
        param([string[]]$Arguments, [string]$OutFile)
        if ($global:FakeEfuseExit -ne 0) {
            return [pscustomobject]@{ ExitCode = $global:FakeEfuseExit; StdOut = ''; StdErr = 'A fatal error occurred' }
        }
        Set-Content -LiteralPath $OutFile -Value $global:FakeEfuseJson -Encoding UTF8
        return [pscustomobject]@{ ExitCode = 0; StdOut = 'Saving efuse values'; StdErr = '' }
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
        $rwMac = $global:FakeBoardInfoMac
        if ($global:FakeReadWriteMac) { $rwMac = $global:FakeReadWriteMac }
        $rwInfo = Get-FakeBoardInfoText -Mac $rwMac
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
                if ($global:FakeSlotReadCorrupt -and $off -eq 0x20000) {
                    $slice[0x10] = $slice[0x10] -bxor 0x01
                }
                [System.IO.File]::WriteAllBytes($out, $slice)
            }
            return [pscustomobject]@{ ExitCode = 0; StdOut = $rwInfo; StdErr = '' }
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
            $eraseEnd = [int]([Math]::Ceiling(($off + $data.Length) / 0x1000) * 0x1000)
            for ($e = $off; $e -lt $eraseEnd; $e++) { $global:FakeFlashBytes[$e] = 0xFF }
            [Array]::Copy($data, 0, $global:FakeFlashBytes, $off, $data.Length)
            if ($global:FakeWriteCorrupt) {
                $global:FakeFlashBytes[0x20010] = $global:FakeFlashBytes[0x20010] -bxor 0x01
            }
            $wInfo = "Chip type:         esp32s3 (revision v0.2)`nFlashing has completed!"
            if ($global:FakeWriteBinBoardInfo) { $wInfo = $rwInfo }
            return [pscustomobject]@{ ExitCode = 0; StdOut = $wInfo; StdErr = '' }
        }
        if ($verb -eq 'monitor') {
            if ($global:FakeMonitorBootLoop) {
                $text = ($bootLine + "`n" + $bootLine + "`n" + ($idleLine + "`n") * 4)
            }
            else {
                $text = ($bootLine + "`n" + ($idleLine + "`n") * 5)
            }
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
        $seeded = [System.IO.File]::ReadAllBytes((Join-Path $run 'backup.bin'))
        for ($i = 0x20000; $i -lt 0x22000; $i++) { $seeded[$i] = 0x5A }
        [System.IO.File]::WriteAllBytes((Join-Path $run 'backup.bin'), $seeded)
        $global:FakeFlashBytes = [byte[]]$seeded.Clone()
        $Script:S8_WorkRoot = $run
        $state = @{
            ReadOnlyComplete = $true
            EfuseOk          = $true
            BackupMd5        = 'abc'
            BackupSha256     = (Get-BytesSha256Hex -Bytes $global:FakeFlashBytes)
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

    It 'Invoke-StockbotFlash entry WhatIf Write never calls write-bin' {
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
        @(Get-S8CallLog | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
    }

    It 'Write refused without ExpectedSha256' {
        $Script:S8_ExpectedSha256 = $null
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Throw '*ExpectedSha256*'
        @(Get-S8CallLog | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
    }

    It 'Write refused when ExpectedSha256 mismatches image' {
        $Script:S8_ExpectedSha256 = ('0' * 64)
        $Script:S8_Confirmation = 'WRITE 0x20000 ' + ('0' * 8)
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Throw '*ExpectedSha256*'
        @(Get-S8CallLog | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
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

    It 'ReadOnly STOP on secure boot eFuse before any flash read' {
        Init-S8TestContext
        $run = New-S8RunDir
        Set-FakeEfuseValue 'SECURE_BOOT_EN' $true
        { Invoke-PhaseReadOnly -RunDir $run -TargetCfg $global:S8Targets } | Should -Throw '*secure boot*'
        @(Get-S8CallLog | Where-Object { $_ -like 'read-flash*' }).Count | Should -Be 0
    }

    It 'ReadOnly STOP when board-info reports secure boot enabled' {
        Init-S8TestContext
        $run = New-S8RunDir
        $global:FakeBoardInfoFixture = Join-Path $global:S8Fixtures 'board_info_secureboot.txt'
        { Invoke-PhaseReadOnly -RunDir $run -TargetCfg $global:S8Targets } | Should -Throw '*board-info*'
        @(Get-S8CallLog | Where-Object { $_ -like 'read-flash*' }).Count | Should -Be 0
        Test-Path -LiteralPath (Join-Path $run 'backup.bin') | Should -Be $false
        $global:FakeBoardInfoFixture = $null
    }

    It 'ReadOnly happy path saves verified state' {
        Init-S8TestContext
        $run = New-S8RunDir
        $global:FakeFlashBytes = [System.IO.File]::ReadAllBytes((Join-Path $global:S8Fixtures 'flash_ota_odd.bin'))
        $global:FakeMd5 = Get-HostBytesMd5Hex -Bytes $global:FakeFlashBytes
        Invoke-PhaseReadOnly -RunDir $run -TargetCfg $global:S8Targets
        $st = ConvertTo-FlashStateHashtable -Raw (Read-FlashState -RunDir $run)
        $st['ReadOnlyComplete'] | Should -Be $true
        $st['BackupMd5'] | Should -Be $global:FakeMd5
        $st['DeviceMac'] | Should -Be 'aa:bb:cc:dd:ee:01'
        @(Get-S8CallLog | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
    }

    It 'ReadOnly to Plan to Write chain with no hand-seeded state' {
        Init-S8TestContext
        $run = New-S8RunDir
        $global:FakeFlashBytes = [System.IO.File]::ReadAllBytes((Join-Path $global:S8Fixtures 'flash_ota_odd.bin'))
        $global:FakeMd5 = Get-HostBytesMd5Hex -Bytes $global:FakeFlashBytes
        $art = Join-Path $run 'artifact'
        New-Item -ItemType Directory -Path $art -Force | Out-Null
        Copy-Item (Join-Path $global:S8Fixtures 'synthetic_firmware_header.bin') (Join-Path $art 'firmware.bin')
        Invoke-PhaseReadOnly -RunDir $run -TargetCfg $global:S8Targets
        $img = Invoke-PhaseDownload -RunDir $run -TargetCfg $global:S8Targets
        Invoke-PhasePlan -RunDir $run -ImageFile $img -TargetCfg $global:S8Targets
        $sha = Get-BytesSha256Hex -Bytes ([System.IO.File]::ReadAllBytes($img))
        $Script:S8_ExpectedSha256 = $sha
        $Script:S8_Confirmation = 'WRITE 0x20000 ' + $sha.Substring(0, 8).ToUpperInvariant()
        Invoke-PhaseWrite -RunDir $run -ImageFile $img -TargetCfg $global:S8Targets
        @(Get-S8CallLog | Where-Object { $_ -like 'write-bin*0x20000*' }).Count | Should -Be 1
    }

    It 'Write STOP when EfuseOk is false' {
        $st = ConvertTo-FlashStateHashtable -Raw (Read-FlashState -RunDir $Script:S8_TestRun)
        $st['EfuseOk'] = $false
        Save-FlashState -RunDir $Script:S8_TestRun -State $st
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = Get-WriteConfirmation
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Throw '*eFuse check not passed*'
        @(Get-S8CallLog | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
    }

    It 'Write STOP when BackupMd5 is missing' {
        $st = ConvertTo-FlashStateHashtable -Raw (Read-FlashState -RunDir $Script:S8_TestRun)
        $st.Remove('BackupMd5')
        Save-FlashState -RunDir $Script:S8_TestRun -State $st
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = Get-WriteConfirmation
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Throw '*backup MD5 missing*'
        @(Get-S8CallLog | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
    }

    It 'Write STOP when TableSha256 is not allow-listed' {
        $st = ConvertTo-FlashStateHashtable -Raw (Read-FlashState -RunDir $Script:S8_TestRun)
        $st['TableSha256'] = ('0' * 64)
        Save-FlashState -RunDir $Script:S8_TestRun -State $st
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = Get-WriteConfirmation
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Throw '*allow-list*'
        @(Get-S8CallLog | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
    }

    It 'Write STOP when PlanComplete is false' {
        $st = ConvertTo-FlashStateHashtable -Raw (Read-FlashState -RunDir $Script:S8_TestRun)
        $st['PlanComplete'] = $false
        Save-FlashState -RunDir $Script:S8_TestRun -State $st
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = Get-WriteConfirmation
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Throw '*Plan required before Write*'
        @(Get-S8CallLog | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
    }

    It 'Write refuses plan offset overlapping NVS at 0x9000' {
        $st = ConvertTo-FlashStateHashtable -Raw (Read-FlashState -RunDir $Script:S8_TestRun)
        $st['PlanOffset'] = '0x9000'
        $st['PlanComplete'] = $true
        Save-FlashState -RunDir $Script:S8_TestRun -State $st
        $sha8 = $global:S8ExpectedSha.Substring(0, 8).ToUpperInvariant()
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = 'WRITE 0x9000 ' + $sha8
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Throw '*overlap*'
        @(Get-S8CallLog | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
    }

    It 'Write refuses plan offset overlapping otadata at 0xF000' {
        $st = ConvertTo-FlashStateHashtable -Raw (Read-FlashState -RunDir $Script:S8_TestRun)
        $st['PlanOffset'] = '0xF000'
        Save-FlashState -RunDir $Script:S8_TestRun -State $st
        $sha8 = $global:S8ExpectedSha.Substring(0, 8).ToUpperInvariant()
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = 'WRITE 0xF000 ' + $sha8
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Throw '*overlap*'
        @(Get-S8CallLog | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
    }

    It 'Write STOP on empty typed confirmation' {
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = ''
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Throw '*confirmation required*'
        @(Get-S8CallLog | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
    }

    It 'Write STOP on whitespace typed confirmation' {
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = '   '
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Throw '*confirmation required*'
        @(Get-S8CallLog | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
    }

    It 'Restore STOP on empty confirmation' {
        $Script:S8_RestoreConfirmation = ''
        { Invoke-PhaseRestore -RunDir $Script:S8_TestRun -TargetCfg $global:S8Targets } | Should -Throw '*confirmation required*'
        @(Get-S8CallLog | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
    }

    It 'Restore STOP on MAC mismatch' {
        $global:FakeBoardInfoMac = 'aa:bb:cc:dd:ee:02'
        $slotSha = Get-BytesSha256Hex -Bytes ($global:FakeFlashBytes[0x20000..(0x20000 + 0x300000 - 1)])
        $Script:S8_RestoreConfirmation = 'RESTORE 0x20000 ' + $slotSha.Substring(0, 8).ToUpperInvariant()
        { Invoke-PhaseRestore -RunDir $Script:S8_TestRun -TargetCfg $global:S8Targets } | Should -Throw '*MAC*'
        @(Get-S8CallLog | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
        $global:FakeBoardInfoMac = 'aa:bb:cc:dd:ee:01'
    }

    It 'Restore refuses NVS otadata offset 0x9000' {
        $st = ConvertTo-FlashStateHashtable -Raw (Read-FlashState -RunDir $Script:S8_TestRun)
        $st['PlanOffset'] = '0x9000'
        Save-FlashState -RunDir $Script:S8_TestRun -State $st
        $slotSize = [int]$st['PlanSlotSize']
        $slotBytes = New-Object byte[] $slotSize
        [Array]::Copy($global:FakeFlashBytes, 0x9000, $slotBytes, 0, $slotSize)
        $slotSha = Get-BytesSha256Hex -Bytes $slotBytes
        $Script:S8_RestoreConfirmation = 'RESTORE 0x9000 ' + $slotSha.Substring(0, 8).ToUpperInvariant()
        { Invoke-PhaseRestore -RunDir $Script:S8_TestRun -TargetCfg $global:S8Targets } | Should -Throw '*overlap*'
        @(Get-S8CallLog | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
    }

    It 'Write STOPs on monitor verdict FAIL' {
        $global:FakeMonitorBootLoop = $true
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = Get-WriteConfirmation
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Throw '*monitor verdict FAIL*'
        $global:FakeMonitorBootLoop = $false
    }

    It 'connect strategies fail during ReadOnly without write-bin' {
        Init-S8TestContext
        $run = New-S8RunDir
        Reset-FakeConnectState
        $global:FakeAllConnectFail = $true
        $Script:S8_DeviceConnectDone = $false
        { Invoke-PhaseReadOnly -RunDir $run -TargetCfg $global:S8Targets } | Should -Throw '*connect failed*'
        @(Get-S8CallLog | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
        $global:FakeAllConnectFail = $false
    }

    It 'ReadOnly STOP when espefuse fails' {
        Init-S8TestContext
        $run = New-S8RunDir
        $global:FakeEfuseExit = 2
        { Invoke-PhaseReadOnly -RunDir $run -TargetCfg $global:S8Targets } | Should -Throw '*espefuse summary failed*'
    }

    It 'ReadOnly STOP when espefuse reports a different MAC' {
        Init-S8TestContext
        $run = New-S8RunDir
        Set-FakeEfuseValue 'MAC' 'aa:bb:cc:dd:ee:09 (OK)'
        { Invoke-PhaseReadOnly -RunDir $run -TargetCfg $global:S8Targets } | Should -Throw '*MAC mismatch*'
    }

    It 'ReadOnly STOP when backup read-flash reports a different MAC' {
        Init-S8TestContext
        $run = New-S8RunDir
        $global:FakeFlashBytes = [System.IO.File]::ReadAllBytes((Join-Path $global:S8Fixtures 'flash_ota_odd.bin'))
        $global:FakeReadWriteMac = 'aa:bb:cc:dd:ee:02'
        { Invoke-PhaseReadOnly -RunDir $run -TargetCfg $global:S8Targets } | Should -Throw '*MAC mismatch*'
        Test-Path -LiteralPath (Join-Path $run 'state.json') | Should -Be $false
    }

    It 'ReadOnly STOP on wrong flash size' {
        Init-S8TestContext
        $run = New-S8RunDir
        $global:FakeFlashBytes = [System.IO.File]::ReadAllBytes((Join-Path $global:S8Fixtures 'flash_ota_odd.bin'))
        $Script:S8_TargetCfg = $global:S8Targets.PSObject.Copy()
        $Script:S8_TargetCfg.flashSize = 4194304
        { Invoke-PhaseReadOnly -RunDir $run -TargetCfg $Script:S8_TargetCfg } | Should -Throw '*flash size*'
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

    It 'STOP when write-bin output has no MAC once a device is bound' {
        $global:FakeWriteBinBoardInfo = $false
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = Get-WriteConfirmation
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Throw '*MAC address*'
        @(Get-S8CallLog | Where-Object { $_ -like 'monitor*' }).Count | Should -Be 0
    }

    It 'STOP when a read-flash in Write reports a different MAC' {
        $global:FakeReadWriteMac = 'aa:bb:cc:dd:ee:02'
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = Get-WriteConfirmation
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Throw '*MAC mismatch*'
        @(Get-S8CallLog | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
    }

    It 'live otadata change since ReadOnly STOPs before write-bin' {
        $global:FakeFlashBytes[0xF000] = $global:FakeFlashBytes[0xF000] -bxor 0x01
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = Get-WriteConfirmation
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Throw '*changed since the ReadOnly backup*'
        $log = Get-S8CallLog
        @($log | Where-Object { $_ -like 'read-flash*0x8000 0x9000*' }).Count | Should -Be 1
        @($log | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
    }

    It 'slot read-back mismatch STOPs before monitor' {
        $global:FakeSlotReadCorrupt = $true
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = Get-WriteConfirmation
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Throw '*read-back*'
        @(Get-S8CallLog | Where-Object { $_ -like 'monitor*' }).Count | Should -Be 0
    }

    It 'slot read-back mismatch STOPs before full re-read and monitor' {
        $global:FakeWriteCorrupt = $true
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = Get-WriteConfirmation
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Throw '*read-back*'
        @(Get-S8CallLog | Where-Object { $_ -like 'read-flash*0x0 0x800000*' }).Count | Should -Be 0
        @(Get-S8CallLog | Where-Object { $_ -like 'monitor*' }).Count | Should -Be 0
        $global:FakeWriteCorrupt = $false
    }

    It 'normal write over an older larger image passes the erased-tail compare' {
        $Script:S8_ExpectedSha256 = $global:S8ExpectedSha
        $Script:S8_Confirmation = Get-WriteConfirmation
        { Invoke-PhaseWrite -RunDir $Script:S8_TestRun -ImageFile $global:S8Img -TargetCfg $global:S8Targets } | Should -Not -Throw
        $global:FakeFlashBytes[0x20000 + 0x400] | Should -Be 0xFF
    }

    It 'ambiguous COM ports STOP instead of picking one' {
        $Script:S8_TestComPorts = @('COM3', 'COM4')
        { Wait-AndResolve-ComPort -TargetCfg $global:S8Targets -TimeoutSeconds 1 } | Should -Throw '*ambiguous*'
    }

    It 'Restore STOPs when backup.bin no longer matches its recorded sha256' {
        $bytes = [System.IO.File]::ReadAllBytes((Join-Path $Script:S8_TestRun 'backup.bin'))
        $bytes[0x30000] = $bytes[0x30000] -bxor 0x01
        [System.IO.File]::WriteAllBytes((Join-Path $Script:S8_TestRun 'backup.bin'), $bytes)
        $Script:S8_RestoreConfirmation = 'RESTORE 0x20000 DEADBEEF'
        { Invoke-PhaseRestore -RunDir $Script:S8_TestRun -TargetCfg $global:S8Targets } | Should -Throw '*does not match the sha256*'
        @(Get-S8CallLog | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
    }

    It 'Restore requires typed confirmation and full slot write' {
        $Script:S8_RestoreConfirmation = 'RESTORE 0x20000 DEADBEEF'
        { Invoke-PhaseRestore -RunDir $Script:S8_TestRun -TargetCfg $global:S8Targets } | Should -Throw '*restore confirmation*'
        @(Get-S8CallLog | Where-Object { $_ -like 'write-bin*' }).Count | Should -Be 0
        $slotSha = Get-BytesSha256Hex -Bytes ($global:FakeFlashBytes[0x20000..(0x20000 + 0x300000 - 1)])
        $Script:S8_RestoreConfirmation = 'RESTORE 0x20000 ' + $slotSha.Substring(0, 8).ToUpperInvariant()
        Invoke-PhaseRestore -RunDir $Script:S8_TestRun -TargetCfg $global:S8Targets
        $log = Get-S8CallLog
        @($log | Where-Object { $_ -like 'write-bin*0x20000*' -and $_ -like '*restore_slot.bin' }).Count | Should -BeGreaterOrEqual 1
        @($log | Where-Object { $_ -like 'read-flash*0x20000 0x300000*restore_readback.bin' }).Count | Should -Be 1
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

    It 'Plan STOPs on default_8mb table not in allow-list' {
        $run = Seed-BackupRun -FlashFixture 'flash_default_8mb.bin'
        $img = Join-Path $run 'artifact/firmware.bin'
        $state = @{ ReadOnlyComplete = $true }
        Save-FlashState -RunDir $run -State $state
        { Invoke-PhasePlan -RunDir $run -ImageFile $img -TargetCfg $global:S8Targets } | Should -Throw '*allowed list*'
    }
}

Describe 'Test-EspflashArgumentsAllowed' {
    It 'rejects write-bin offset below 0x9000' {
        { Test-EspflashArgumentsAllowed -Arguments @('write-bin', '--chip', 'esp32s3', '--port', 'COM3', '--before', 'usb-reset', '--after', 'no-reset', '0x8000', 'f.bin') } | Should -Throw '*0x9000*'
    }
    It 'rejects read write without --after no-reset' {
        { Test-EspflashArgumentsAllowed -Arguments @('write-bin', '--chip', 'esp32s3', '--port', 'COM3', '--before', 'usb-reset', '0x20000', 'f.bin') } | Should -Throw '*no-reset*'
    }
    It 'rejects forbidden flash verb' {
        { Test-EspflashArgumentsAllowed -Arguments @('flash', '--chip', 'esp32s3', '--port', 'COM3', '--before', 'usb-reset', '--after', 'no-reset', 'firmware.bin') } | Should -Throw '*forbidden*'
    }
}

Describe 'Download via RunId' {
    It 'RunId download verifies ExpectedSha256 via invoker seam' {
        Init-S8TestContext
        $run = New-S8RunDir
        $imgBytes = [System.IO.File]::ReadAllBytes((Join-Path $global:S8Fixtures 'synthetic_firmware_header.bin'))
        $wantSha = Get-BytesSha256Hex -Bytes $imgBytes
        $Script:S8_RunId = '12345'
        $Script:S8_ExpectedSha256 = $wantSha
        $Script:S8_RunDownloadInvoker = {
            param([string]$RunId, [string]$Name, [string]$Dest)
            New-Item -ItemType Directory -Path $Dest -Force | Out-Null
            Copy-Item (Join-Path $global:S8Fixtures 'synthetic_firmware_header.bin') (Join-Path $Dest 'firmware.bin')
        }
        $path = Invoke-PhaseDownload -RunDir $run -TargetCfg $global:S8Targets
        $path | Should -Be (Join-Path $run 'artifact/firmware.bin')
        $Script:S8_RunDownloadInvoker = $null
        $Script:S8_RunId = $null
    }
}

Describe 'Run folder reuse' {
    BeforeEach {
        Init-S8TestContext
        $Script:S8_WorkRoot = New-S8RunDir
    }

    It 'Plan, Write and Restore reuse the newest folder that has state.json' {
        $old = Join-Path $Script:S8_WorkRoot '20260101-090000_local'
        $new = Join-Path $Script:S8_WorkRoot '20260102-090000_local'
        $noState = Join-Path $Script:S8_WorkRoot '20260103-090000_local'
        $other = Join-Path $Script:S8_WorkRoot '20260104-090000_123'
        foreach ($d in @($old, $new, $noState, $other)) { New-Item -ItemType Directory -Path $d | Out-Null }
        foreach ($d in @($old, $new, $other)) { Save-FlashState -RunDir $d -State @{ ReadOnlyComplete = $true } }
        Get-RunDirectory -RunKey 'local' -Reuse | Should -Be $new
    }

    It 'STOPs when no earlier run folder exists' {
        { Get-RunDirectory -RunKey 'local' -Reuse } | Should -Throw '*no earlier run folder*'
    }

    It 'ReadOnly folder is found by a later Plan in a new process' {
        $global:FakeFlashBytes = [System.IO.File]::ReadAllBytes((Join-Path $global:S8Fixtures 'flash_ota_odd.bin'))
        $global:FakeMd5 = Get-HostBytesMd5Hex -Bytes $global:FakeFlashBytes
        $first = Get-RunDirectory -RunKey 'local'
        Invoke-PhaseReadOnly -RunDir $first -TargetCfg $global:S8Targets
        $global:FakeMd5 = 'deadbeefdeadbeefdeadbeefdeadbeef'
        Init-S8TestContext
        $Script:S8_WorkRoot = Split-Path -Parent $first
        $reused = Get-RunDirectory -RunKey 'local' -Reuse
        $reused | Should -Be $first
        $img = Join-Path $global:S8Fixtures 'synthetic_firmware_header.bin'
        Invoke-PhasePlan -RunDir $reused -ImageFile $img -TargetCfg $global:S8Targets
        (ConvertTo-FlashStateHashtable -Raw (Read-FlashState -RunDir $reused))['PlanOffset'] | Should -Be '0x20000'
    }
}
