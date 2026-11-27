#requires -Version 5.1
# Dot-sourced by Invoke-StockbotFlash.ps1 and integration tests.

if (-not (Test-Path -LiteralPath 'variable:Script:S8_EspflashInvoker')) {
    $Script:S8_EspflashInvoker = $null
}
if (-not (Test-Path -LiteralPath 'variable:Script:S8_EspefuseInvoker')) {
    $Script:S8_EspefuseInvoker = $null
}

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
    $Script:S8_AllowEnvTestSeams = $false
    $Script:S8_RunDownloadInvoker = $null
}

function Test-EnvTestSeamAllowed {
    param([string]$Name)
    if ($Script:S8_AllowEnvTestSeams) {
        return $true
    }
    if (Test-Path -LiteralPath ("Env:$Name")) {
        throw ('STOP: {0} is set but this is not a test run (test seams are disabled in production)' -f $Name)
    }
    return $false
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

function Find-LatestRunDirectory {
    param([string]$RunKey)
    $workRoot = $Script:S8_WorkRoot
    if (-not (Test-Path -LiteralPath $workRoot)) {
        return $null
    }
    $suffix = '_' + $RunKey
    $candidates = @(Get-ChildItem -LiteralPath $workRoot -Directory | Where-Object {
            $_.Name.EndsWith($suffix) -and (Test-Path -LiteralPath (Join-Path $_.FullName 'state.json'))
        } | Sort-Object Name -Descending)
    if ($candidates.Count -eq 0) {
        return $null
    }
    return $candidates[0].FullName
}

function Protect-RunDirectory {
    param([string]$Dir)
    if (-not (Get-Command icacls -ErrorAction SilentlyContinue)) {
        return
    }
    & icacls $Dir /inheritance:r | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw ('STOP: could not restrict access to the run folder {0} (icacls exit {1}); the backup holds Wi-Fi and token data' -f $Dir, $LASTEXITCODE)
    }
    & icacls $Dir /grant:r ($env:USERNAME + ':(OI)(CI)F') | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw ('STOP: could not grant the current user access to the run folder {0} (icacls exit {1})' -f $Dir, $LASTEXITCODE)
    }
}

function Get-RunDirectory {
    param(
        [string]$RunKey,
        [switch]$Reuse
    )
    if ($Reuse) {
        $existing = Find-LatestRunDirectory -RunKey $RunKey
        if (-not $existing) {
            throw ('STOP: no earlier run folder for "{0}" under {1}. Run -Phase All (or ReadOnly) first.' -f $RunKey, $Script:S8_WorkRoot)
        }
        return $existing
    }
    $workRoot = $Script:S8_WorkRoot
    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
    $dir = Join-Path $workRoot ($stamp + '_' + $RunKey)
    if (-not (Test-Path -LiteralPath $dir)) {
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
        Protect-RunDirectory -Dir $dir
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
    if ($verb -eq 'read-flash') {
        $addr = @($Arguments | Where-Object { $_ -match '^0x[0-9a-fA-F]+$' } | Select-Object -First 1)
        if ($addr.Count -eq 0) {
            throw 'STOP: read-flash missing offset'
        }
        $a = [Convert]::ToUInt32($addr[0].Substring(2), 16)
        if ($a -lt 0x8000 -and $a -ne 0) {
            throw 'STOP: read below 0x8000 except full image'
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
            $portToken = '(' + $n + ')'
            $devs = Get-CimInstance Win32_PnPEntity -ErrorAction SilentlyContinue | Where-Object {
                $null -ne $_.Name -and $_.Name.Contains($portToken)
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
    $result = Invoke-EspflashProcess -Arguments $Arguments
    Assert-EspflashResultBoundDevice -Arguments $Arguments -Result $result
    return $result
}

function Assert-EspflashResultBoundDevice {
    param(
        [string[]]$Arguments,
        $Result
    )
    if (-not $Script:S8_BoundMac) {
        return
    }
    if ($Result.ExitCode -ne 0) {
        return
    }
    $verb = $Arguments[0]
    if (@('board-info', 'read-flash', 'write-bin') -notcontains $verb) {
        return
    }
    $text = ([string]$Result.StdOut + "`n" + [string]$Result.StdErr)
    $id = Parse-BoardInfoIdentity -Text $text
    Test-DeviceMacBinding -ObservedMac $id.Mac -BoundMac $Script:S8_BoundMac -ExpectedMac $Script:S8_ExpectedMac | Out-Null
}

function Invoke-EspflashProcess {
    param([string[]]$Arguments)
    if ($null -ne $Script:S8_EspflashInvoker) {
        return & $Script:S8_EspflashInvoker $Arguments
    }
    $esp = Get-Command espflash -ErrorAction SilentlyContinue
    if (-not $esp) {
        throw 'STOP: espflash was not found on PATH. Install espflash 3.3.0 and try again.'
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

function Invoke-EspefuseSummary {
    param(
        [Parameter(Mandatory)]
        [string]$OutFile,
        $TargetCfg
    )
    if (-not $TargetCfg) { $TargetCfg = $Script:S8_TargetCfg }
    $port = Wait-AndResolve-ComPort -TargetCfg $TargetCfg
    $command = @($TargetCfg.espefuseCommand)
    $arguments = @($command[1..($command.Count - 1)]) + @('--chip', $TargetCfg.chip, '--port', $port, '--before', 'usb_reset', 'summary', '--format', 'json', '--file', $OutFile)
    [void]$Script:S8_EspflashCallLog.Add(('espefuse ' + (@('--chip', $TargetCfg.chip, '--port', $port, 'summary', '--format', 'json') -join ' ')))
    if ($null -ne $Script:S8_EspefuseInvoker) {
        $r = & $Script:S8_EspefuseInvoker $arguments $OutFile
    }
    else {
        $exe = Get-Command $command[0] -ErrorAction SilentlyContinue
        if (-not $exe) {
            throw ('STOP: {0} was not found on PATH, so the eFuse check cannot run. Install Python and esptool {1} (pip install esptool=={1}) and try again.' -f $command[0], $TargetCfg.espefuseVersion)
        }
        $psi = New-Object System.Diagnostics.ProcessStartInfo
        $psi.FileName = $exe.Source
        $psi.Arguments = ($arguments -join ' ')
        $psi.RedirectStandardOutput = $true
        $psi.RedirectStandardError = $true
        $psi.UseShellExecute = $false
        $p = [System.Diagnostics.Process]::Start($psi)
        $stdout = $p.StandardOutput.ReadToEnd()
        $stderr = $p.StandardError.ReadToEnd()
        $p.WaitForExit()
        $r = [pscustomobject]@{ ExitCode = $p.ExitCode; StdOut = $stdout; StdErr = $stderr }
    }
    if ($r.ExitCode -ne 0) {
        throw ('STOP: espefuse summary failed (exit {0}), so the eFuse check cannot run' -f $r.ExitCode)
    }
    if (-not (Test-Path -LiteralPath $OutFile)) {
        throw 'STOP: espefuse did not write its summary file, so the eFuse check cannot run'
    }
    return (Get-Content -Raw -LiteralPath $OutFile)
}

function Assert-EfuseSecurity {
    param(
        [Parameter(Mandatory)]
        [string]$RunDir,
        $TargetCfg
    )
    $summaryPath = Join-Path $RunDir 'efuse_summary.json'
    $json = Invoke-EspefuseSummary -OutFile $summaryPath -TargetCfg $TargetCfg
    $ef = Parse-EfuseSecuritySummary -SummaryJson $json
    if (-not $ef.Mac) {
        throw 'STOP: espefuse summary has no MAC, so it cannot be tied to the bound device'
    }
    Test-DeviceMacBinding -ObservedMac $ef.Mac -BoundMac $Script:S8_BoundMac -ExpectedMac $Script:S8_ExpectedMac | Out-Null
    return $ef
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
    $id = Parse-BoardInfoIdentity -Text $text
    $null = Test-BoardInfoSecurityAllowed -Text $text
    Test-BoardIdentityMatchesTarget -Identity $id -Chip $TargetCfg.chip -FlashSize ([int64]$TargetCfg.flashSize) | Out-Null
    $bound = $Script:S8_BoundMac
    if (-not $bound -and $State -and $State['DeviceMac']) {
        $bound = $State['DeviceMac']
        $Script:S8_BoundMac = $bound
        $Script:S8_BoundChip = $State['DeviceChip']
    }
    Test-DeviceMacBinding -ObservedMac $id.Mac -BoundMac $bound -ExpectedMac $Script:S8_ExpectedMac | Out-Null
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

function Assert-EspflashVersionInstalled {
    param($TargetCfg)
    $want = [string]$TargetCfg.espflashVersion
    $esp = Get-Command espflash -ErrorAction SilentlyContinue
    if (-not $esp) {
        throw ('STOP: espflash was not found on PATH. Install espflash {0} (or use -InstallPrereqs) and try again.' -f $want)
    }
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $esp.Source
    $psi.Arguments = '--version'
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.UseShellExecute = $false
    $p = [System.Diagnostics.Process]::Start($psi)
    $text = ($p.StandardOutput.ReadToEnd() + $p.StandardError.ReadToEnd()).Trim()
    $p.WaitForExit()
    if ($text -notmatch [regex]::Escape($want)) {
        throw ('STOP: espflash version must be {0} (reported: {1})' -f $want, $text)
    }
}

function Install-StockbotEspflashPrereq {
    param($TargetCfg)
    $toolsRoot = Join-Path $env:LOCALAPPDATA 'StockbotFlash\tools'
    $verDir = Join-Path $toolsRoot ('espflash-' + $TargetCfg.espflashVersion)
    $exe = Get-ChildItem -LiteralPath $verDir -Filter 'espflash.exe' -Recurse -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($exe) {
        Write-FlashLog ('espflash already present: ' + $exe.FullName)
        return $exe.FullName
    }
    if (-not $TargetCfg.espflashZipUrl -or -not $TargetCfg.espflashZipSha256) {
        throw 'STOP: espflash zip URL or sha256 is not configured in targets.json'
    }
    New-Item -ItemType Directory -Path $verDir -Force | Out-Null
    $zipPath = Join-Path $env:TEMP ('espflash-' + $TargetCfg.espflashVersion + '.zip')
    Write-FlashLog ('downloading espflash {0}...' -f $TargetCfg.espflashVersion)
    Invoke-WebRequest -Uri $TargetCfg.espflashZipUrl -OutFile $zipPath
    $zipHash = (Get-BytesSha256Hex -Bytes ([System.IO.File]::ReadAllBytes($zipPath))).ToLowerInvariant()
    $wantHash = [string]$TargetCfg.espflashZipSha256.ToLowerInvariant()
    if ($zipHash -ne $wantHash) {
        throw ('STOP: espflash zip sha256 mismatch (expected {0}, got {1})' -f $wantHash, $zipHash)
    }
    Expand-Archive -LiteralPath $zipPath -DestinationPath $verDir -Force
    $exe = Get-ChildItem -LiteralPath $verDir -Filter 'espflash.exe' -Recurse | Select-Object -First 1
    if (-not $exe) {
        throw 'STOP: espflash.exe was not found in the downloaded zip'
    }
    Write-FlashLog ('installed espflash to ' + $exe.FullName)
    return $exe.FullName
}

function Invoke-GhRunDownload {
    param(
        [string]$RunId,
        [string]$ArtifactName,
        [string]$Destination
    )
    if ($null -ne $Script:S8_RunDownloadInvoker) {
        return & $Script:S8_RunDownloadInvoker $RunId $ArtifactName $Destination
    }
    $gh = Get-Command gh -ErrorAction SilentlyContinue
    if (-not $gh) {
        throw 'STOP: gh CLI is not on PATH; use -ImagePath or install GitHub CLI'
    }
    & $gh.Source 'run' 'download' $RunId '-n' $ArtifactName '-D' $Destination
    if ($LASTEXITCODE -ne 0) {
        throw ('STOP: gh run download failed for run {0}' -f $RunId)
    }
}

function Invoke-PhasePrereqs {
    if ($Script:S8_InstallPrereqs) {
        $null = Install-StockbotEspflashPrereq -TargetCfg $Script:S8_TargetCfg
    }
    Assert-EspflashVersionInstalled -TargetCfg $Script:S8_TargetCfg
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
    if ($Script:S8_RunId) {
        if (-not (Test-Path -LiteralPath $dest)) {
            New-Item -ItemType Directory -Path $dest -Force | Out-Null
        }
        Invoke-GhRunDownload -RunId $Script:S8_RunId -ArtifactName $TargetCfg.artifactName -Destination $dest
        $out = Join-Path $dest $TargetCfg.binFile
        if (-not (Test-Path -LiteralPath $out)) {
            throw ('STOP: downloaded artifact is missing {0}' -f $TargetCfg.binFile)
        }
        if ($Script:S8_ExpectedSha256) {
            $got = (Get-BytesSha256Hex -Bytes (Get-ImageBytes -Path $out)).ToLowerInvariant()
            $want = $Script:S8_ExpectedSha256.ToLowerInvariant()
            if ($got -ne $want) {
                throw ('STOP: downloaded image sha256 mismatch (expected {0}, got {1})' -f $want, $got)
            }
        }
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
    $null = Assert-EfuseSecurity -RunDir $RunDir -TargetCfg $TargetCfg
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
    $imgSha = Get-BytesSha256Hex -Bytes $image
    Write-FlashLog ('plan: {0} at 0x{1:X} (otadata seq {2}), image sha256 {3}' -f $slot.SlotName, $slot.Offset, $slot.OtaSeq, $imgSha)
    Write-FlashLog ('to write, pass: -ExpectedSha256 {0} -Confirmation ''WRITE 0x{1:X} {2}''' -f $imgSha, $slot.Offset, $imgSha.Substring(0, 8).ToUpperInvariant())
}

function Assert-LiveMetadataMatchesBackup {
    param(
        [string]$RunDir,
        [byte[]]$BackupBytes,
        $PartitionEntries,
        $TargetCfg
    )
    $start = [uint32]0x8000
    $end = [uint64]0x9000
    foreach ($p in $PartitionEntries) {
        $isNvs = ($p.Type -eq 1 -and $p.Subtype -eq 2)
        $isOtadata = ($p.Type -eq 1 -and $p.Subtype -eq 0)
        if ($isNvs -or $isOtadata) {
            $pEnd = [uint64]$p.Offset + [uint64]$p.Size
            if ($pEnd -gt $end) { $end = $pEnd }
        }
    }
    $length = [uint32]($end - $start)
    $livePath = Join-Path $RunDir 'live_metadata.bin'
    $r = Invoke-Espflash -Arguments @('read-flash', '--chip', $TargetCfg.chip, '--port', 'COM0', ('0x{0:X}' -f $start), ('0x{0:X}' -f $length), $livePath)
    if ($r.ExitCode -ne 0) {
        throw 'STOP: could not re-read the partition table, NVS and otadata before writing'
    }
    $live = Get-ImageBytes -Path $livePath
    if ($live.Length -ne $length) {
        throw 'STOP: live re-read of the partition table, NVS and otadata returned the wrong length'
    }
    for ($i = 0; $i -lt $length; $i++) {
        if ($live[$i] -ne $BackupBytes[$start + $i]) {
            throw ('STOP: the device changed since the ReadOnly backup (partition table, NVS or otadata differ at 0x{0:X}). Run -Phase ReadOnly -Redo and -Phase Plan again.' -f ($start + $i))
        }
    }
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
    if ([string]::IsNullOrWhiteSpace($typed) -and $null -ne $Script:S8_PSCaller) {
        $typed = Read-Host -Prompt ('Type: WRITE ' + $offsetHex + ' ' + $imgSha.Substring(0, 8).ToUpperInvariant())
    }
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
    Assert-LiveMetadataMatchesBackup -RunDir $RunDir -BackupBytes $before -PartitionEntries $entries -TargetCfg $TargetCfg
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
    if (-not (Test-Path -LiteralPath $backupPath)) {
        throw ('STOP: backup.bin is missing from {0}' -f $RunDir)
    }
    $backup = Get-ImageBytes -Path $backupPath
    if (-not $state['BackupSha256']) {
        throw 'STOP: state.json has no BackupSha256, so backup.bin cannot be verified'
    }
    if ((Get-BytesSha256Hex -Bytes $backup) -ne $state['BackupSha256']) {
        throw 'STOP: backup.bin does not match the sha256 recorded at ReadOnly time'
    }
    $offset = [Convert]::ToUInt32($state['PlanOffset'].Substring(2), 16)
    $slotSize = [int]$state['PlanSlotSize']
    $slotBytes = New-Object byte[] $slotSize
    [Array]::Copy($backup, $offset, $slotBytes, 0, $slotSize)
    $slotSha = Get-BytesSha256Hex -Bytes $slotBytes
    $typed = $Script:S8_RestoreConfirmation
    if ([string]::IsNullOrWhiteSpace($typed) -and $null -ne $Script:S8_PSCaller) {
        $typed = Read-Host -Prompt ('Type: RESTORE ' + $state['PlanOffset'] + ' ' + $slotSha.Substring(0, 8).ToUpperInvariant())
    }
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
    $readPath = Join-Path $RunDir 'restore_readback.bin'
    $rr = Invoke-Espflash -Arguments @('read-flash', '--chip', $chip, '--port', 'COM0', $state['PlanOffset'], ('0x{0:X}' -f $slotSize), $readPath)
    if ($rr.ExitCode -ne 0) {
        throw 'STOP: restore read-back failed'
    }
    if ((Get-BytesSha256Hex -Bytes (Get-ImageBytes -Path $readPath)) -ne $slotSha) {
        throw 'STOP: restore read-back does not match the backup slot'
    }
    Write-FlashLog 'restore verified'
}