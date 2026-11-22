#requires -Version 5.1
<#
.SYNOPSIS
    StickS3 (sticks3_u173) Windows flash CLI for slice S8.

.DESCRIPTION
    Never writes bootloader (0x0), partition table (0x8000), otadata, or NVS.
    Does not invoke "espflash flash" or "pio upload". Uses espflash read-flash /
    write-bin / monitor with --after no-reset on read and write (espflash 3.3.0).

.NOTES
    Hardware flashing is CJ-gated; run ReadOnly and Plan first on a bench device.
#>
[CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = 'High')]
param(
    [ValidateSet('Prereqs', 'Download', 'ReadOnly', 'Plan', 'Write', 'Restore', 'All')]
    [string]$Phase = 'All',
    [ValidateSet('sticks3_u173')]
    [string]$Target = 'sticks3_u173',
    [string]$RunId,
    [string]$ImagePath,
    [string]$ExpectedSha256,
    [string]$Port,
    [string]$WorkRoot,
    [switch]$InstallPrereqs,
    [switch]$Redo,
    [string]$Confirmation,
    [string]$RestoreConfirmation
)

Set-StrictMode -Version 2
$ErrorActionPreference = 'Stop'

$ScriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$ModulePath = if ($env:STOCKBOT_MODULE_PATH) { $env:STOCKBOT_MODULE_PATH } else { Join-Path $ScriptRoot 'StockbotFlash.psm1' }
Import-Module $ModulePath -Force

$TargetsPath = Join-Path $ScriptRoot 'targets.json'
$TargetsJson = Get-Content -Raw -Path $TargetsPath | ConvertFrom-Json
$TargetCfg = $TargetsJson.targets.$Target

if (-not $WorkRoot) {
    $WorkRoot = Join-Path $env:LOCALAPPDATA 'StockbotFlash'
}

$Script:EspflashInvoker = $null
if ($env:STOCKBOT_ESPFLASH_INVOKER) {
    $Script:EspflashInvoker = [scriptblock]::Create($env:STOCKBOT_ESPFLASH_INVOKER)
}

function Write-FlashLog {
    param([string]$Message)
    Write-Host $Message
}

function Get-RunDirectory {
    param([string]$RunKey)
    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
    $dir = Join-Path $WorkRoot ($stamp + '_' + $RunKey)
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

function Save-FlashState {
    param(
        [string]$RunDir,
        [hashtable]$State
    )
    $path = Get-StatePath -RunDir $RunDir
    $json = ($State | ConvertTo-Json -Depth 6)
    Set-Content -LiteralPath $path -Value $json -Encoding UTF8
}

function Invoke-Espflash {
    param(
        [Parameter(Mandatory)]
        [string[]]$Arguments
    )
    if ($null -ne $Script:EspflashInvoker) {
        return & $Script:EspflashInvoker $Arguments
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

function Find-StickS3ComPort {
    $names = [System.IO.Ports.SerialPort]::GetPortNames() | Sort-Object -Unique
    $candidates = New-Object System.Collections.ArrayList
    foreach ($n in $names) {
        $ok = $false
        if (Get-Command Get-CimInstance -ErrorAction SilentlyContinue) {
            $devs = Get-CimInstance Win32_PnPEntity -ErrorAction SilentlyContinue | Where-Object {
                $_.Name -like ('*' + $n + '*')
            }
            foreach ($d in $devs) {
                if ($d.DeviceID -match 'VID_303A&PID_1001') {
                    $ok = $true
                }
            }
        }
        if ($ok) {
            [void]$candidates.Add($n)
        }
    }
    return ,$candidates.ToArray()
}

function Resolve-ComPort {
    param([string]$Explicit)
    if ($Explicit) {
        return $Explicit
    }
    $c = Find-StickS3ComPort
    if ($c.Count -eq 0) {
        Write-FlashLog 'STOP: no StickS3 COM port (VID_303A&PID_1001). Hold power until green LED blinks, use USB-C data cable, retry.'
        exit 2
    }
    if ($c.Count -gt 1) {
        Write-FlashLog ('STOP: ambiguous COM ports: ' + ($c -join ', '))
        exit 2
    }
    return $c[0]
}

function Invoke-PhasePrereqs {
    $missing = New-Object System.Collections.ArrayList
    foreach ($tool in @('python', 'gh')) {
        if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) {
            [void]$missing.Add($tool)
        }
    }
    if (-not (Get-Command espflash -ErrorAction SilentlyContinue)) {
        [void]$missing.Add('espflash 3.3.0')
    }
    $espefuse = $false
    foreach ($c in @('espefuse', 'espefuse.py')) {
        if (Get-Command $c -ErrorAction SilentlyContinue) { $espefuse = $true }
    }
    if (-not $espefuse) {
        [void]$missing.Add('espefuse (pip install esptool)')
    }
    if ($missing.Count -gt 0) {
        Write-FlashLog ('Missing prerequisites: ' + ($missing -join '; '))
        if ($InstallPrereqs -and $PSCmdlet.ShouldProcess('host', 'Install pinned espflash/gh')) {
            Write-FlashLog 'InstallPrereqs would download espflash zip (sha256 pinned in targets.json) and suggest: winget install GitHub.cli; pip install esptool'
        }
        if ($missing.Count -gt 0) {
            throw 'STOP: prerequisites not satisfied'
        }
    }
    $port = Resolve-ComPort -Explicit $Port
    Write-FlashLog ('COM port: ' + $port)
}

function Get-ImageBytes {
    param([string]$Path)
    return [System.IO.File]::ReadAllBytes($Path)
}

function Invoke-PhaseDownload {
    param([string]$RunDir)
    $dest = Join-Path $RunDir 'artifact'
    if ((-not $Redo) -and (Test-Path (Join-Path $dest $TargetCfg.binFile))) {
        Write-FlashLog 'Download skipped (artifact present)'
        return (Join-Path $dest $TargetCfg.binFile)
    }
    if ($ImagePath) {
        if (-not (Test-Path -LiteralPath $ImagePath)) {
            throw 'STOP: ImagePath not found'
        }
        if (-not (Test-Path -LiteralPath $dest)) {
            New-Item -ItemType Directory -Path $dest -Force | Out-Null
        }
        $out = Join-Path $dest $TargetCfg.binFile
        Copy-Item -LiteralPath $ImagePath -Destination $out -Force
        return $out
    }
    if (-not $RunId) {
        throw 'STOP: RunId or -ImagePath required for Download'
    }
    if ($PSCmdlet.ShouldProcess($dest, ('gh run download ' + $RunId))) {
        if (-not $InstallPrereqs) {
            $st = & gh auth status 2>&1
            if ($LASTEXITCODE -ne 0) {
                throw 'STOP: gh auth required for artifact download'
            }
        }
        if (-not (Test-Path -LiteralPath $dest)) {
            New-Item -ItemType Directory -Path $dest -Force | Out-Null
        }
        & gh run download $RunId -R Rhionin/stockbot -n $TargetCfg.artifactName -D $dest
        if ($LASTEXITCODE -ne 0) {
            throw 'STOP: artifact download failed'
        }
    }
    $bin = Join-Path $dest $TargetCfg.binFile
    if (-not (Test-Path -LiteralPath $bin)) {
        throw 'STOP: firmware.bin missing after download'
    }
    return $bin
}

function Invoke-PhaseReadOnly {
    param([string]$RunDir)
    $state = @{}
    if (Test-Path (Get-StatePath -RunDir $RunDir)) {
        $existing = Read-FlashState -RunDir $RunDir
        foreach ($p in $existing.PSObject.Properties) {
            $state[$p.Name] = $p.Value
        }
    }
    if ($state.ReadOnlyComplete -eq $true -and -not $Redo) {
        Write-FlashLog 'ReadOnly skipped (state.json)'
        return
    }
    $port = Resolve-ComPort -Explicit $Port
    if ($PSCmdlet.ShouldProcess($port, 'espflash board-info')) {
        $r = Invoke-Espflash -Arguments @('board-info', '--chip', 'esp32s3', '--port', $port, '--after', 'no-reset')
        if ($r.ExitCode -ne 0) {
            throw 'STOP: board-info failed'
        }
        if ($r.StdOut -notmatch '8MB') {
            throw 'STOP: expected 8MB flash'
        }
    }
    $efuseText = $env:STOCKBOT_EFUSE_SUMMARY
    if (-not $efuseText) {
        if (Get-Command espefuse -ErrorAction SilentlyContinue) {
            $efuseText = (& espefuse --chip esp32s3 --port $port summary 2>&1 | Out-String)
        }
        elseif (Get-Command espefuse.py -ErrorAction SilentlyContinue) {
            $efuseText = (& espefuse.py --chip esp32s3 --port $port summary 2>&1 | Out-String)
        }
        else {
            throw 'STOP: espefuse unavailable'
        }
    }
    $ef = Parse-EfuseSecuritySummary -SummaryText $efuseText
    if (-not $ef.Allowed) {
        throw 'STOP: secure boot or flash encryption enabled'
    }
    $backup = Join-Path $RunDir 'backup.bin'
    if ($PSCmdlet.ShouldProcess($port, 'read-flash 0x0 0x800000')) {
        if ($null -ne $Script:EspflashInvoker) {
            $r = Invoke-Espflash -Arguments @('read-flash', '--chip', 'esp32s3', '--port', $port, '--after', 'no-reset', '0x0', '0x800000', $backup)
        }
        else {
            $r = Invoke-Espflash -Arguments @('read-flash', '--chip', 'esp32s3', '--port', $port, '--after', 'no-reset', '0x0', '0x800000', $backup)
        }
        if ($r.ExitCode -ne 0) {
            throw 'STOP: backup read-flash failed'
        }
    }
    if (Test-Path -LiteralPath $backup) {
        $bytes = Get-ImageBytes -Path $backup
        $state.BackupSha256 = Get-BytesSha256Hex -Bytes $bytes
        $state.BootloaderHeader = ([BitConverter]::ToString($bytes[0..3])).Replace('-', '').ToLowerInvariant()
        $state.EfuseOk = $true
        $state.ReadOnlyComplete = $true
        Save-FlashState -RunDir $RunDir -State $state
        Write-FlashLog ('backup sha256=' + $state.BackupSha256)
        Write-FlashLog ('bootloader header=' + $state.BootloaderHeader)
    }
}

function Invoke-PhasePlan {
    param(
        [string]$RunDir,
        [string]$ImageFile
    )
    $state = @{}
    $raw = Read-FlashState -RunDir $RunDir
    foreach ($p in $raw.PSObject.Properties) {
        $state[$p.Name] = $p.Value
    }
    if (-not $state.ReadOnlyComplete) {
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
    foreach ($w in $hdr.Warnings) { Write-FlashLog ('WARNING: ' + $w) }
    Test-FlashPlanConstraints -AppOffset $slot.Offset -ImageLength $image.Length -SlotSize $slot.Size -PartitionEntries $entries
    $imgSha = Get-BytesSha256Hex -Bytes $image
    if ($ExpectedSha256) {
        if ($imgSha -ne $ExpectedSha256.ToLowerInvariant()) {
            throw 'STOP: image sha256 mismatch'
        }
    }
    else {
        Write-FlashLog ('image sha256=' + $imgSha + ' (UNVERIFIED)')
    }
    $oldPrefix = $flash[$slot.Offset..($slot.Offset + 15)]
    Write-FlashLog ('APP_OFFSET=0x{0:X}' -f $slot.Offset)
    Write-FlashLog ('APP_SIZE=' + $slot.Size)
    Write-FlashLog ('slot=' + $slot.SlotName)
    Write-FlashLog ('table sha256=' + $allow.Sha256)
    Write-FlashLog ('old slot prefix=' + ([BitConverter]::ToString($oldPrefix)).Replace('-', '').ToLowerInvariant())
    $confirm = ('WRITE 0x{0:X} {1}' -f $slot.Offset, $imgSha.Substring(0, 8).ToUpperInvariant())
    Write-FlashLog ('typed confirmation: ' + $confirm)
    $state.PlanComplete = $true
    $state.PlanOffset = ('0x{0:X}' -f $slot.Offset)
    $state.PlanSlotSize = $slot.Size
    $state.PlanImageSha256 = $imgSha
    $state.TableSha256 = $allow.Sha256
    Save-FlashState -RunDir $RunDir -State $state
}

function Invoke-PhaseWrite {
    param(
        [string]$RunDir,
        [string]$ImageFile
    )
    if (-not $ExpectedSha256) {
        throw 'STOP: -ExpectedSha256 is mandatory for Write'
    }
    $state = @{}
    $raw = Read-FlashState -RunDir $RunDir
    foreach ($p in $raw.PSObject.Properties) {
        $state[$p.Name] = $p.Value
    }
    if (-not $state.PlanComplete) {
        throw 'STOP: Plan required before Write'
    }
    if ($state.BackupSha256 -eq $null) {
        throw 'STOP: verified backup missing'
    }
    $image = Get-ImageBytes -Path $ImageFile
    $imgSha = Get-BytesSha256Hex -Bytes $image
    if ($imgSha -ne $ExpectedSha256.ToLowerInvariant()) {
        throw 'STOP: ExpectedSha256 mismatch'
    }
    $offsetHex = $state.PlanOffset
    $offset = [Convert]::ToUInt32($offsetHex.Substring(2), 16)
    $slotSize = [uint32]$state.PlanSlotSize
    Test-WriteRangeAllowed -Offset $offset -Length $image.Length -PartitionEntries (Read-PartitionTable -TableBytes (Get-ImageBytes -Path (Join-Path $RunDir 'backup.bin'))[0x8000..(0x8000+0xC00-1)])
    $typed = $Confirmation
    if (-not $typed) {
        $typed = Read-Host 'Type confirmation'
    }
    if (-not (Test-TypedConfirmation -UserInput $typed -ExpectedOffsetHex $offsetHex -ImageSha256Hex $imgSha)) {
        throw 'STOP: typed confirmation mismatch'
    }
    $port = Resolve-ComPort -Explicit $Port
    $backupPath = Join-Path $RunDir 'backup.bin'
    $before = Get-ImageBytes -Path $backupPath
    if (-not $PSCmdlet.ShouldProcess($port, 'write-bin')) {
        Write-FlashLog 'WhatIf: skipping hardware write'
        return
    }
    $r = Invoke-Espflash -Arguments @('write-bin', '--chip', 'esp32s3', '--port', $port, '--after', 'no-reset', ('0x{0:X}' -f $offset), $ImageFile)
    if ($r.ExitCode -ne 0) {
        throw 'STOP: write-bin failed'
    }
    $afterPath = Join-Path $RunDir 'after.bin'
    $r2 = Invoke-Espflash -Arguments @('read-flash', '--chip', 'esp32s3', '--port', $port, '--after', 'no-reset', '0x0', '0x800000', $afterPath)
    if ($r2.ExitCode -ne 0) {
        throw 'STOP: post-write read-flash failed'
    }
    $after = Get-ImageBytes -Path $afterPath
    Test-OutsideSlotUnchanged -BeforeBytes $before -AfterBytes $after -SlotOffset $offset -ImageLength $image.Length -SlotSize $slotSize
    Write-FlashLog 'Write verification OK (outside slot unchanged)'
}

function Invoke-PhaseRestore {
    param([string]$RunDir)
    $state = @{}
    $raw = Read-FlashState -RunDir $RunDir
    foreach ($p in $raw.PSObject.Properties) {
        $state[$p.Name] = $p.Value
    }
    if (-not $state.PlanOffset) {
        throw 'STOP: no plan offset for restore'
    }
    $backupPath = Join-Path $RunDir 'backup.bin'
    $backup = Get-ImageBytes -Path $backupPath
    $offset = [Convert]::ToUInt32($state.PlanOffset.Substring(2), 16)
    $slotSize = [int]$state.PlanSlotSize
    $slotBytes = New-Object byte[] $slotSize
    [Array]::Copy($backup, $offset, $slotBytes, 0, $slotSize)
    $slotSha = Get-BytesSha256Hex -Bytes $slotBytes
    $typed = $RestoreConfirmation
    if (-not $typed) {
        $typed = Read-Host 'Type restore confirmation'
    }
    if (-not (Test-TypedRestoreConfirmation -UserInput $typed -ExpectedOffsetHex $state.PlanOffset -SlotSha256Hex $slotSha)) {
        throw 'STOP: restore confirmation mismatch'
    }
    if (-not $PSCmdlet.ShouldProcess($Port, 'restore slot write-bin')) {
        Write-FlashLog 'WhatIf: restore not written'
        return
    }
    $port = Resolve-ComPort -Explicit $Port
    $tmp = Join-Path $RunDir 'restore_slot.bin'
    [System.IO.File]::WriteAllBytes($tmp, $slotBytes)
    Test-WriteRangeAllowed -Offset $offset -Length $slotSize -PartitionEntries (Read-PartitionTable -TableBytes $backup[0x8000..(0x8000+0xC00-1)])
    $r = Invoke-Espflash -Arguments @('write-bin', '--chip', 'esp32s3', '--port', $port, '--after', 'no-reset', $state.PlanOffset, $tmp)
    if ($r.ExitCode -ne 0) {
        throw 'STOP: restore write-bin failed'
    }
}

$runKey = if ($RunId) { $RunId } else { 'local' }
$runDir = Get-RunDirectory -RunKey $runKey

switch ($Phase) {
    'Prereqs' { Invoke-PhasePrereqs }
    'Download' {
        $img = Invoke-PhaseDownload -RunDir $runDir
        Write-FlashLog ('image=' + $img)
    }
    'ReadOnly' { Invoke-PhaseReadOnly -RunDir $runDir }
    'Plan' {
        $img = Invoke-PhaseDownload -RunDir $runDir
        Invoke-PhasePlan -RunDir $runDir -ImageFile $img
    }
    'Write' {
        $img = Invoke-PhaseDownload -RunDir $runDir
        Invoke-PhaseWrite -RunDir $runDir -ImageFile $img
    }
    'Restore' { Invoke-PhaseRestore -RunDir $runDir }
    'All' {
        Invoke-PhasePrereqs
        $img = Invoke-PhaseDownload -RunDir $runDir
        Invoke-PhaseReadOnly -RunDir $runDir
        Invoke-PhasePlan -RunDir $runDir -ImageFile $img
    }
}