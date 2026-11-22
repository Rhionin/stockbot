#requires -Version 5.1
# Dot-sourced by Invoke-StockbotFlash.ps1 and integration tests.

function Initialize-StockbotFlashContext {
    param(
        $Cmdlet,
        [string]$Target = 'sticks3_u173',
        [string]$RunId,
        [string]$ImagePath,
        [string]$ExpectedSha256,
        [string]$ExpectedMac,
        [string]$Port,
        [string]$WorkRoot,
        $TargetCfg,
        [switch]$InstallPrereqs,
        [switch]$Redo,
        [string]$Confirmation,
        [string]$RestoreConfirmation
    )
    $Script:S8_PSCaller = $Cmdlet
    $Script:S8_Target = $Target
    $Script:S8_RunId = $RunId
    $Script:S8_ImagePath = $ImagePath
    $Script:S8_ExpectedSha256 = $ExpectedSha256
    $Script:S8_ExpectedMac = $ExpectedMac
    $Script:S8_PortHint = $Port
    $Script:S8_Port = $Port
    $Script:S8_WorkRoot = $WorkRoot
    $Script:S8_TargetCfg = $TargetCfg
    $Script:S8_InstallPrereqs = [bool]$InstallPrereqs
    $Script:S8_Redo = [bool]$Redo
    $Script:S8_Confirmation = $Confirmation
    $Script:S8_RestoreConfirmation = $RestoreConfirmation
    $Script:S8_BoundMac = $null
    $Script:S8_BoundChip = $null
    $Script:S8_ConnectStrategy = $null
    $Script:S8_DeviceConnectDone = $false
    $Script:S8_WhatIf = $false
    if ($null -ne $Cmdlet -and $Cmdlet.MyInvocation.BoundParameters.ContainsKey('WhatIf')) {
        $Script:S8_WhatIf = [bool]$Cmdlet.MyInvocation.BoundParameters['WhatIf']
    }
    $Script:S8_EspflashCallLog = New-Object System.Collections.ArrayList
    $Script:S8_TestComPorts = $null
}

function Test-S8ShouldProcess {
    param(
        [string]$Target,
        [string]$Action
    )
    if ($Script:S8_WhatIf) {
        return $false
    }
    if ($null -eq $Script:S8_PSCaller) {
        return $true
    }
    return $Script:S8_PSCaller.ShouldProcess($Target, $Action)
}

function Write-FlashLog {
    param([string]$Message)
    Write-Host $Message
}

function Get-RunDirectory {
    param([string]$RunKey)
    $workRoot = $Script:S8_WorkRoot
    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
    $dir = Join-Path $workRoot ($stamp + '_' + $RunKey)
    if (-not (Test-Path -LiteralPath $dir)) {
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
        if (Get-Command icacls -ErrorAction SilentlyContinue) {
            & icacls $dir /inheritance:r | Out-Null
            & icacls $dir /grant:r ($env:USERNAME + ':(OI)(CI)F') | Out-Null
        }
    }
    return $dir
}

function Get-StatePath {
    param([string]$RunDir)
    return Join-Path $RunDir 'state.json'
}

function Read-FlashState {
    param([string]$RunDir)
    $path = Get-StatePath -RunDir $RunDir
    if (-not (Test-Path -LiteralPath $path)) {
        return @{}
    }
    return (Get-Content -Raw -Path $path | ConvertFrom-Json)
}

function ConvertTo-FlashStateHashtable {
    param($Raw)
    $state = @{}
    if ($null -eq $Raw) {
        return $state
    }
    if ($Raw -is [hashtable]) {
        foreach ($k in $Raw.Keys) {
            $state[$k] = $Raw[$k]
        }
        return $state
    }
    foreach ($p in $Raw.PSObject.Properties) {
        $state[$p.Name] = $p.Value
    }
    return $state
}

function Save-FlashState {
    param(
        [string]$RunDir,
        [hashtable]$State
    )
    $path = Get-StatePath -RunDir $RunDir
    $json = ($State | ConvertTo-Json -Depth 6)
    Set-Content -LiteralPath $path -Value $json -Encoding UTF8
}

function Get-HostBytesMd5Hex {
    param([byte[]]$Bytes)
    $md5 = [System.Security.Cryptography.MD5]::Create()
    try {
        $hash = $md5.ComputeHash($Bytes)
    }
    finally {
        $md5.Dispose()
    }
    return ([BitConverter]::ToString($hash)).Replace('-', '').ToLowerInvariant()
}

function Parse-EspflashChecksumMd5 {
    param([string]$Text)
    if ($Text -match '([0-9a-fA-F]{32})') {
        return $Matches[1].ToLowerInvariant()
    }
    throw 'STOP: could not parse checksum-md5 output'
}

function Test-EspflashArgumentsAllowed {
    param(
        [string[]]$Arguments,
        [switch]$IsMonitor
    )
    if ($null -eq $Arguments -or $Arguments.Count -eq 0) {
        return
    }
    $verb = $Arguments[0]
    if ($verb -eq 'flash' -or ($Arguments -join ' ') -match '(^|\s)pio\s+upload') {
        throw 'STOP: forbidden full flash command'
    }
    $needsNoReset = @('read-flash', 'write-bin', 'checksum-md5', 'board-info')
    if ($needsNoReset -contains $verb) {
        $joined = $Arguments -join ' '
        if ($joined -notmatch '--after\s+no-reset') {
            throw 'STOP: missing --after no-reset'
        }
        if ($joined -notmatch '--before\s+\S+') {
            throw 'STOP: missing --before reset option'
        }
    }
    if ($verb -eq 'write-bin') {
        $offIdx = -1
        for ($i = 0; $i -lt $Arguments.Count; $i++) {
            if ($Arguments[$i] -match '^0x[0-9a-fA-F]+$') {
                $offIdx = $i
                break
            }
        }
        if ($offIdx -lt 0) {
            throw 'STOP: write-bin missing offset'
        }
        $off = [Convert]::ToUInt32($Arguments[$offIdx].Substring(2), 16)
        if ($off -lt 0x9000) {
            throw 'STOP: write offset below 0x9000'
        }
    }
    if ($verb -eq 'read-flash' -and $Arguments.Count -ge 3) {
        $addr = $Arguments[2]
        if ($addr -match '^0x([0-9a-fA-F]+)$') {
            $a = [Convert]::ToUInt32($Matches[1], 16)
            if ($a -lt 0x9000 -and $a -ne 0) {
                throw 'STOP: read below 0x9000 except full image'
            }
        }
    }
}

function Get-S8CallLog {
    return @($Script:S8_EspflashCallLog.ToArray())
}

function Set-EspflashPortInArguments {
    param(
        [string[]]$Arguments,
        [string]$Port
    )
    $out = New-Object System.Collections.ArrayList
    for ($i = 0; $i -lt $Arguments.Count; $i++) {
        if ($Arguments[$i] -eq '--port' -and ($i + 1) -lt $Arguments.Count) {
            [void]$out.Add('--port')
            [void]$out.Add($Port)
            $i++
        }
        else {
            [void]$out.Add($Arguments[$i])
        }
    }
  return ,@($out.ToArray())
}

function Add-EspflashResetFlags {
    param(
        [string[]]$Arguments,
        [string]$Before,
        [string]$After
    )
    $filtered = New-Object System.Collections.ArrayList
    for ($i = 0; $i -lt $Arguments.Count; $i++) {
        if ($Arguments[$i] -eq '--before' -or $Arguments[$i] -eq '--after') {
            $i++
            continue
        }
        [void]$filtered.Add($Arguments[$i])
    }
    $args2 = @($filtered.ToArray())
    $verb = $args2[0]
    if ($verb -eq 'monitor') {
        return ,$args2
    }
    $with = New-Object System.Collections.ArrayList
    $inserted = $false
    for ($i = 0; $i -lt $args2.Count; $i++) {
        [void]$with.Add($args2[$i])
        if (-not $inserted -and $args2[$i] -eq '--port' -and ($i + 1) -lt $args2.Count) {
            [void]$with.Add($args2[$i + 1])
            [void]$with.Add('--before')
            [void]$with.Add($Before)
            if ($After) {
                [void]$with.Add('--after')
                [void]$with.Add($After)
            }
            $inserted = $true
            $i++
        }
    }
    if (-not $inserted) {
        [void]$with.Add('--before')
        [void]$with.Add($Before)
        if ($After) {
            [void]$with.Add('--after')
            [void]$with.Add($After)
        }
    }
    return ,@($with.ToArray())
}

function Get-AvailableStickComPorts {
    param($TargetCfg)
    if ($null -ne $Script:S8_TestComPorts) {
        return @($Script:S8_TestComPorts)
    }
    $names = [System.IO.Ports.SerialPort]::GetPortNames() | Sort-Object -Unique
    $candidates = New-Object System.Collections.ArrayList
    $patterns = New-Object System.Collections.ArrayList
    foreach ($d in $TargetCfg.usbSerialDevices) {
        $vid = $d.vid.ToUpperInvariant()
        $pid = $d.pid.ToUpperInvariant()
        [void]$patterns.Add(('VID_' + $vid + '&PID_' + $pid))
    }
    foreach ($n in $names) {
        $ok = $false
        if (Get-Command Get-CimInstance -ErrorAction SilentlyContinue) {
            $devs = Get-CimInstance Win32_PnPEntity -ErrorAction SilentlyContinue | Where-Object {
                $_.Name -like ('*' + $n + '*')
            }
            foreach ($d in $devs) {
                foreach ($pat in $patterns) {
                    if ($d.DeviceID -match [regex]::Escape($pat)) {
                        $ok = $true
                    }
                }
            }
        }
        if ($ok) {
            [void]$candidates.Add($n)
        }
    }
    return ,@($candidates.ToArray())
}

function Wait-AndResolve-ComPort {
    param(
        $TargetCfg,
        [int]$TimeoutSeconds = 10
    )
    if (-not $TargetCfg) {
        $TargetCfg = $Script:S8_TargetCfg
    }
    if (-not $TimeoutSeconds) {
        $TimeoutSeconds = [int]$TargetCfg.portPollSeconds
    }
    if (-not $TimeoutSeconds) {
        $TimeoutSeconds = 10
    }
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        $found = @(Get-AvailableStickComPorts -TargetCfg $TargetCfg)
        if ($found.Count -eq 1) {
            $Script:S8_Port = $found[0]
            return $found[0]
        }
        if ($found.Count -gt 1) {
            throw ('STOP: ambiguous COM ports: ' + ($found -join ', '))
        }
        if ($Script:S8_PortHint -and ($found.Count -eq 0)) {
            Start-Sleep -Milliseconds 250
        }
        else {
            Start-Sleep -Milliseconds 250
        }
    } while ((Get-Date) -lt $deadline)
    if ($Script:S8_PortHint) {
        throw ('STOP: no StickS3 COM port (hint was ' + $Script:S8_PortHint + ')')
    }
    throw 'STOP: no StickS3 COM port'
}

function Invoke-Espflash {
    param(
        [Parameter(Mandatory)]
        [string[]]$Arguments,
        [string]$ConnectBeforeOverride,
        [switch]$SkipPortResolve,
        [switch]$IsMonitor
    )
    if (-not $SkipPortResolve) {
        $port = Wait-AndResolve-ComPort -TargetCfg $Script:S8_TargetCfg
        Write-FlashLog ('COM resolved: ' + $port)
        $Arguments = Set-EspflashPortInArguments -Arguments $Arguments -Port $port
    }
    if (-not $IsMonitor) {
        $before = if ($ConnectBeforeOverride) { $ConnectBeforeOverride } else { 'usb-reset' }
        $Arguments = Add-EspflashResetFlags -Arguments $Arguments -Before $before -After 'no-reset'
    }
    else {
        $port = Wait-AndResolve-ComPort -TargetCfg $Script:S8_TargetCfg
        $Arguments = Set-EspflashPortInArguments -Arguments $Arguments -Port $port
    }
    Test-EspflashArgumentsAllowed -Arguments $Arguments -IsMonitor:$IsMonitor
    [void]$Script:S8_EspflashCallLog.Add(($Arguments -join ' '))
    if ($null -ne $Script:S8_EspflashInvoker) {
        return & $Script:S8_EspflashInvoker $Arguments
    }
    $esp = Get-Command espflash -ErrorAction SilentlyContinue
    if (-not $esp) {
        throw 'espflash not found on PATH (pin 3.3.0; use -InstallPrereqs or install manually)'
    }
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $esp.Source
    $psi.Arguments = ($Arguments -join ' ')
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.UseShellExecute = $false
    $p = [System.Diagnostics.Process]::Start($psi)
    $stdout = $p.StandardOutput.ReadToEnd()
    $stderr = $p.StandardError.ReadToEnd()
    $p.WaitForExit()
    return [pscustomobject]@{
        ExitCode = $p.ExitCode
        StdOut   = $stdout
        StdErr   = $stderr
    }
}

function Invoke-EspflashMonitorCapture {
    param(
        [Parameter(Mandatory)]
        [int]$TimeoutSeconds,
        [Parameter(Mandatory)]
        [string]$OutFile,
        $TargetCfg
    )
    if (-not $TargetCfg) { $TargetCfg = $Script:S8_TargetCfg }
    $chip = $TargetCfg.chip
    $port = Wait-AndResolve-ComPort -TargetCfg $TargetCfg
    $arguments = @('monitor', '--non-interactive', '--chip', $chip, '--port', $port)
    if ($null -ne $Script:S8_EspflashInvoker) {
        $r = Invoke-Espflash -Arguments $arguments -IsMonitor
        $text = ($r.StdOut + "`n" + $r.StdErr)
        Set-Content -LiteralPath $OutFile -Value $text -Encoding UTF8
        return $r
    }
    $esp = Get-Command espflash -ErrorAction SilentlyContinue
    if (-not $esp) {
        throw 'espflash not found for monitor'
    }
    Test-EspflashArgumentsAllowed -Arguments $arguments -IsMonitor
    [void]$Script:S8_EspflashCallLog.Add(($arguments -join ' '))
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $esp.Source
    $psi.Arguments = ($arguments -join ' ')
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.UseShellExecute = $false
    $p = [System.Diagnostics.Process]::Start($psi)
    if (-not $p.WaitForExit($TimeoutSeconds * 1000)) {
        try { $p.Kill() } catch { }
        $p.WaitForExit(5000) | Out-Null
    }
    $stdout = $p.StandardOutput.ReadToEnd()
    $stderr = $p.StandardError.ReadToEnd()
    $text = ($stdout + "`n" + $stderr)
    Set-Content -LiteralPath $OutFile -Value $text -Encoding UTF8
    return [pscustomobject]@{
        ExitCode = $p.ExitCode
        StdOut   = $stdout
        StdErr   = $stderr
    }
}

function Assert-DeviceSecurityDualSource {
    param(
        [string]$BoardInfoText,
        [string]$EfuseSummaryText
    )
    Test-BoardInfoSecurityAllowed -Text $BoardInfoText
    if ([string]::IsNullOrWhiteSpace($EfuseSummaryText)) {
        throw 'STOP: espefuse unavailable'
    }
    $ef = Parse-EfuseSecuritySummary -SummaryText $EfuseSummaryText
    if (-not $ef.Allowed) {
        throw 'STOP: secure boot or flash encryption enabled (espefuse)'
    }
}

function Invoke-PhaseDeviceIdentityCheck {
    param(
        $TargetCfg,
        [switch]$Bind,
        $State
    )
    $chip = $TargetCfg.chip
    $r = Invoke-Espflash -Arguments @('board-info', '--chip', $chip, '--port', 'COM0')
    if ($r.ExitCode -ne 0) {
        throw 'STOP: board-info failed'
    }
    $text = ($r.StdOut + "`n" + $r.StdErr)
    Test-BoardInfoSecurityAllowed -Text $text
    $id = Parse-BoardInfoIdentity -Text $text
    $bound = $Script:S8_BoundMac
    if (-not $bound -and $State -and $State['DeviceMac']) {
        $bound = $State['DeviceMac']
        $Script:S8_BoundMac = $bound
        $Script:S8_BoundChip = $State['DeviceChip']
    }
    Test-DeviceMacBinding -ObservedMac $id.Mac -BoundMac $bound -ExpectedMac $Script:S8_ExpectedMac
    if ($Bind) {
        $Script:S8_BoundMac = $id.Mac
        $Script:S8_BoundChip = $id.Chip
    }
    return $id
}

function Invoke-DeviceConnect {
    param($TargetCfg)
    if ($Script:S8_DeviceConnectDone) {
        return
    }
    $chip = $TargetCfg.chip
    $succeeded = $false
    foreach ($attempt in Get-ConnectAttempts) {
        Write-FlashLog ('connect strategy: ' + $attempt.Name)
        $port = Wait-AndResolve-ComPort -TargetCfg $TargetCfg
        $r = Invoke-Espflash -Arguments @('board-info', '--chip', $chip, '--port', $port) -ConnectBeforeOverride $attempt.BeforeArg
        if ($r.ExitCode -eq 0) {
            $text = ($r.StdOut + "`n" + $r.StdErr)
            try {
                Test-BoardInfoSecurityAllowed -Text $text | Out-Null
                $null = Parse-BoardInfoIdentity -Text $text
            }
            catch {
                Write-FlashLog ('connect rejected: ' + $_.Exception.Message)
                continue
            }
            $Script:S8_ConnectStrategy = $attempt.Name
            Write-FlashLog ('connect chosen: ' + $attempt.Name)
            $null = Wait-AndResolve-ComPort -TargetCfg $TargetCfg
            $succeeded = $true
            break
        }
    }
    if (-not $succeeded) {
        Write-FlashLog (Get-DownloadModeInstructionText)
        throw 'STOP: device connect failed (all strategies exhausted)'
    }
    $Script:S8_DeviceConnectDone = $true
}

function Get-ImageBytes {
    param([string]$Path)
    return [System.IO.File]::ReadAllBytes($Path)
}

function Assert-WritePrerequisitesFromState {
    param([hashtable]$State, $TargetCfg)
    if (-not $State['ReadOnlyComplete']) {
        throw 'STOP: verified backup missing (ReadOnly incomplete)'
    }
    if ($State['EfuseOk'] -ne $true) {
        throw 'STOP: eFuse check not passed'
    }
    if (-not $State['BackupMd5']) {
        throw 'STOP: verified backup MD5 missing'
    }
    if (-not $State['TableSha256']) {
        throw 'STOP: allowed table not recorded in plan'
    }
    $allowed = $false
    foreach ($a in $TargetCfg.allowedTableSha256) {
        if ($State['TableSha256'] -eq $a) { $allowed = $true }
    }
    if (-not $allowed) {
        throw 'STOP: partition table not in allow-list'
    }
}

function Invoke-PhasePrereqs {
    $port = Wait-AndResolve-ComPort -TargetCfg $Script:S8_TargetCfg
    if ($Script:S8_PortHint) {
        Write-FlashLog ('COM hint: ' + $Script:S8_PortHint)
    }
    Write-FlashLog ('COM port: ' + $port)
}

function Invoke-PhaseDownload {
    param(
        [string]$RunDir,
        $TargetCfg
    )
    $dest = Join-Path $RunDir 'artifact'
    if ((-not $Script:S8_Redo) -and (Test-Path (Join-Path $dest $TargetCfg.binFile))) {
        return (Join-Path $dest $TargetCfg.binFile)
    }
    if ($Script:S8_ImagePath) {
        if (-not (Test-Path -LiteralPath $Script:S8_ImagePath)) {
            throw 'STOP: ImagePath not found'
        }
        if (-not (Test-Path -LiteralPath $dest)) {
            New-Item -ItemType Directory -Path $dest -Force | Out-Null
        }
        $out = Join-Path $dest $TargetCfg.binFile
        Copy-Item -LiteralPath $Script:S8_ImagePath -Destination $out -Force
        return $out
    }
    throw 'STOP: RunId or ImagePath required for Download'
}

function Invoke-PhaseReadOnly {
    param(
        [string]$RunDir,
        $TargetCfg
    )
    $state = ConvertTo-FlashStateHashtable -Raw (Read-FlashState -RunDir $RunDir)
    if ($state['ReadOnlyComplete'] -eq $true -and -not $Script:S8_Redo) {
        return
    }
    $chip = $TargetCfg.chip
    $port = Wait-AndResolve-ComPort -TargetCfg $TargetCfg
    if (-not (Test-S8ShouldProcess -Target $port -Action 'board-info')) {
        return
    }
    Invoke-DeviceConnect -TargetCfg $TargetCfg
    $id = Invoke-PhaseDeviceIdentityCheck -TargetCfg $TargetCfg -Bind -State $state
    $efuseText = $env:STOCKBOT_EFUSE_SUMMARY
    if (-not $efuseText) {
        throw 'STOP: espefuse unavailable (set STOCKBOT_EFUSE_SUMMARY in tests)'
    }
    $boardSample = Invoke-Espflash -Arguments @('board-info', '--chip', $chip, '--port', 'COM0')
    Assert-DeviceSecurityDualSource -BoardInfoText ($boardSample.StdOut + "`n" + $boardSample.StdErr) -EfuseSummaryText $efuseText
    $backup = Join-Path $RunDir 'backup.bin'
    $flashLen = ('0x{0:X}' -f $TargetCfg.flashSize)
    $r = Invoke-Espflash -Arguments @('read-flash', '--chip', $chip, '--port', 'COM0', '0x0', $flashLen, $backup)
    if ($r.ExitCode -ne 0) {
        throw 'STOP: backup read-flash failed'
    }
    if (-not (Test-Path -LiteralPath $backup)) {
        throw 'STOP: backup missing'
    }
    $bytes = Get-ImageBytes -Path $backup
    $hostMd5 = Get-HostBytesMd5Hex -Bytes $bytes
    $md5r = Invoke-Espflash -Arguments @('checksum-md5', '--chip', $chip, '--port', 'COM0', '--address', '0x0', '--length', $flashLen)
    if ($md5r.ExitCode -ne 0) {
        throw 'STOP: device checksum-md5 failed'
    }
    $devMd5 = Parse-EspflashChecksumMd5 -Text ($md5r.StdOut + $md5r.StdErr)
    if ($hostMd5 -ne $devMd5) {
        throw 'STOP: backup MD5 mismatch'
    }
    $state['BackupMd5'] = $hostMd5
    $state['BackupSha256'] = Get-BytesSha256Hex -Bytes $bytes
    $state['EfuseOk'] = $true
    $state['DeviceMac'] = $id.Mac
    $state['DeviceChip'] = $id.Chip
    $state['ReadOnlyComplete'] = $true
    Save-FlashState -RunDir $RunDir -State $state
}

function Invoke-PhasePlan {
    param(
        [string]$RunDir,
        [string]$ImageFile,
        $TargetCfg
    )
    $state = ConvertTo-FlashStateHashtable -Raw (Read-FlashState -RunDir $RunDir)
    if (-not $state['ReadOnlyComplete']) {
        throw 'STOP: ReadOnly must complete before Plan'
    }
    $backup = Join-Path $RunDir 'backup.bin'
    $flash = Get-ImageBytes -Path $backup
    $table = $flash[0x8000..(0x8000 + 0xC00 - 1)]
    $entries = Read-PartitionTable -TableBytes $table
    $allow = Test-TableAllowed -TableBytes $table -AllowedSha256 @($TargetCfg.allowedTableSha256)
    $otaPart = $entries | Where-Object { $_.Type -eq 1 -and $_.Subtype -eq 0 } | Select-Object -First 1
    if (-not $otaPart) {
        throw 'STOP: no otadata partition'
    }
    $odataBytes = $flash[$otaPart.Offset..($otaPart.Offset + $otaPart.Size - 1)]
    $sectors = Read-Otadata -OtadataBytes $odataBytes
    $slot = Select-AppSlot -PartitionEntries $entries -OtadataSectors $sectors
    $image = Get-ImageBytes -Path $ImageFile
    $hdr = Test-ImageHeader -ImageBytes $image -SlotSize $slot.Size
    foreach ($e in $hdr.Errors) { throw ('STOP: ' + $e) }
    Test-FlashPlanConstraints -AppOffset $slot.Offset -ImageLength $image.Length -SlotSize $slot.Size -PartitionEntries $entries
    $state['PlanComplete'] = $true
    $state['PlanOffset'] = ('0x{0:X}' -f $slot.Offset)
    $state['PlanSlotSize'] = $slot.Size
    $state['TableSha256'] = $allow.Sha256
    Save-FlashState -RunDir $RunDir -State $state
}

function Invoke-PhaseWrite {
    param(
        [string]$RunDir,
        [string]$ImageFile,
        $TargetCfg
    )
    if (-not $Script:S8_ExpectedSha256) {
        throw 'STOP: -ExpectedSha256 is mandatory for Write'
    }
    $state = ConvertTo-FlashStateHashtable -Raw (Read-FlashState -RunDir $RunDir)
    if (-not $state['PlanComplete']) {
        throw 'STOP: Plan required before Write'
    }
    Assert-WritePrerequisitesFromState -State $state -TargetCfg $TargetCfg
    $image = Get-ImageBytes -Path $ImageFile
    $imgSha = Get-BytesSha256Hex -Bytes $image
    if ($imgSha -ne $Script:S8_ExpectedSha256.ToLowerInvariant()) {
        throw 'STOP: ExpectedSha256 mismatch'
    }
    $offsetHex = $state['PlanOffset']
    $offset = [Convert]::ToUInt32($offsetHex.Substring(2), 16)
    $slotSize = [uint32]$state['PlanSlotSize']
    $tableBytes = (Get-ImageBytes -Path (Join-Path $RunDir 'backup.bin'))[0x8000..(0x8000 + 0xC00 - 1)]
    $entries = Read-PartitionTable -TableBytes $tableBytes
    Test-WriteRangeAllowed -Offset $offset -Length $image.Length -PartitionEntries $entries
    $typed = $Script:S8_Confirmation
    if ([string]::IsNullOrWhiteSpace($typed)) {
        throw 'STOP: typed confirmation required'
    }
    if (-not (Test-TypedConfirmation -UserInput $typed -ExpectedOffsetHex $offsetHex -ImageSha256Hex $imgSha)) {
        throw 'STOP: typed confirmation mismatch'
    }
    $chip = $TargetCfg.chip
    $null = Invoke-PhaseDeviceIdentityCheck -TargetCfg $TargetCfg -State $state
    $port = Wait-AndResolve-ComPort -TargetCfg $TargetCfg
    $backupPath = Join-Path $RunDir 'backup.bin'
    $before = Get-ImageBytes -Path $backupPath
    if (-not (Test-S8ShouldProcess -Target $port -Action 'write-bin')) {
        Write-FlashLog 'WhatIf: skipping hardware write'
        return
    }
    $r = Invoke-Espflash -Arguments @('write-bin', '--chip', $chip, '--port', 'COM0', ('0x{0:X}' -f $offset), $ImageFile)
    if ($r.ExitCode -ne 0) {
        throw 'STOP: write-bin failed'
    }
    $rlen = $image.Length
    $slotRead = Join-Path $RunDir 'slot_readback.bin'
    $rSlot = Invoke-Espflash -Arguments @('read-flash', '--chip', $chip, '--port', 'COM0', ('0x{0:X}' -f $offset), ('0x{0:X}' -f $rlen), $slotRead)
    if ($rSlot.ExitCode -ne 0) {
        throw 'STOP: slot read-back failed'
    }
    $readBack = Get-ImageBytes -Path $slotRead
    for ($i = 0; $i -lt $image.Length; $i++) {
        if ($readBack[$i] -ne $image[$i]) {
            throw 'STOP: slot read-back prefix mismatch'
        }
    }
    $afterPath = Join-Path $RunDir 'after.bin'
    $flashLen = ('0x{0:X}' -f $TargetCfg.flashSize)
    $r2 = Invoke-Espflash -Arguments @('read-flash', '--chip', $chip, '--port', 'COM0', '0x0', $flashLen, $afterPath)
    if ($r2.ExitCode -ne 0) {
        throw 'STOP: post-write read-flash failed'
    }
    $after = Get-ImageBytes -Path $afterPath
    Test-OutsideSlotUnchanged -BeforeBytes $before -AfterBytes $after -SlotOffset $offset -ImageLength $image.Length -SlotSize $slotSize
    $mon1 = Join-Path $RunDir 'monitor1.log'
    $mon2 = Join-Path $RunDir 'monitor2.log'
    Invoke-EspflashMonitorCapture -TimeoutSeconds 60 -OutFile $mon1 -TargetCfg $TargetCfg | Out-Null
    Invoke-EspflashMonitorCapture -TimeoutSeconds 30 -OutFile $mon2 -TargetCfg $TargetCfg | Out-Null
    $cap1 = Get-Content -Raw -LiteralPath $mon1
    $cap2 = Get-Content -Raw -LiteralPath $mon2
    $targetId = $Script:S8_Target
    if (-not $targetId) { $targetId = 'sticks3_u173' }
    $verdict = Get-MonitorVerdict -Capture1Text $cap1 -Capture2Text $cap2 -TargetId $targetId
    foreach ($line in $verdict.MarkerLines) {
        Write-FlashLog ('marker: ' + $line)
    }
    if ($verdict.Pass) {
        Write-FlashLog 'monitor verdict: PASS'
    }
    else {
        Write-FlashLog ('monitor verdict: FAIL (' + $verdict.Reason + ')')
        throw 'STOP: monitor verdict FAIL'
    }
}

function Invoke-PhaseRestore {
    param(
        [string]$RunDir,
        $TargetCfg
    )
    $state = ConvertTo-FlashStateHashtable -Raw (Read-FlashState -RunDir $RunDir)
    if (-not $state['PlanOffset']) {
        throw 'STOP: no plan offset for restore'
    }
    $backupPath = Join-Path $RunDir 'backup.bin'
    $backup = Get-ImageBytes -Path $backupPath
    $offset = [Convert]::ToUInt32($state['PlanOffset'].Substring(2), 16)
    $slotSize = [int]$state['PlanSlotSize']
    $slotBytes = New-Object byte[] $slotSize
    [Array]::Copy($backup, $offset, $slotBytes, 0, $slotSize)
    $slotSha = Get-BytesSha256Hex -Bytes $slotBytes
    $typed = $Script:S8_RestoreConfirmation
    if ([string]::IsNullOrWhiteSpace($typed)) {
        throw 'STOP: restore confirmation required'
    }
    if (-not (Test-TypedRestoreConfirmation -UserInput $typed -ExpectedOffsetHex $state['PlanOffset'] -SlotSha256Hex $slotSha)) {
        throw 'STOP: restore confirmation mismatch'
    }
    $chip = $TargetCfg.chip
    $null = Invoke-PhaseDeviceIdentityCheck -TargetCfg $TargetCfg -State $state
    $port = Wait-AndResolve-ComPort -TargetCfg $TargetCfg
    if (-not (Test-S8ShouldProcess -Target $port -Action 'restore write-bin')) {
        return
    }
    $tmp = Join-Path $RunDir 'restore_slot.bin'
    [System.IO.File]::WriteAllBytes($tmp, $slotBytes)
    Test-WriteRangeAllowed -Offset $offset -Length $slotSize -PartitionEntries (Read-PartitionTable -TableBytes $backup[0x8000..(0x8000 + 0xC00 - 1)])
    $r = Invoke-Espflash -Arguments @('write-bin', '--chip', $chip, '--port', 'COM0', $state['PlanOffset'], $tmp)
    if ($r.ExitCode -ne 0) {
        throw 'STOP: restore write-bin failed'
    }
}