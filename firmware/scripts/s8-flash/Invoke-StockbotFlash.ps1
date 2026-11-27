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
    [string]$ExpectedMac,
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
Import-Module (Join-Path $ScriptRoot 'StockbotFlash.psm1') -Force

$TargetsPath = Join-Path $ScriptRoot 'targets.json'
$TargetsJson = Get-Content -Raw -Path $TargetsPath | ConvertFrom-Json
$TargetCfg = $TargetsJson.targets.$Target

if (-not $WorkRoot) {
    $WorkRoot = Join-Path $env:LOCALAPPDATA 'StockbotFlash'
}

. (Join-Path $ScriptRoot 'StockbotFlash.Cli.ps1')

Initialize-StockbotFlashContext -Cmdlet $PSCmdlet -Target $Target -RunId $RunId -ImagePath $ImagePath `
    -ExpectedSha256 $ExpectedSha256 -ExpectedMac $ExpectedMac -Port $Port -WorkRoot $WorkRoot `
    -TargetCfg $TargetCfg -InstallPrereqs:$InstallPrereqs -Redo:$Redo -Confirmation $Confirmation `
    -RestoreConfirmation $RestoreConfirmation

$runKey = if ($RunId) { $RunId } else { 'local' }

switch ($Phase) {
    'Prereqs' { Invoke-PhasePrereqs }
    'Download' {
        $runDir = Get-RunDirectory -RunKey $runKey
        $img = Invoke-PhaseDownload -RunDir $runDir -TargetCfg $TargetCfg
        Write-FlashLog ('image=' + $img)
    }
    'ReadOnly' {
        $runDir = Get-RunDirectory -RunKey $runKey
        Invoke-PhaseReadOnly -RunDir $runDir -TargetCfg $TargetCfg
    }
    'Plan' {
        $runDir = Get-RunDirectory -RunKey $runKey -Reuse
        $img = Invoke-PhaseDownload -RunDir $runDir -TargetCfg $TargetCfg
        Invoke-PhasePlan -RunDir $runDir -ImageFile $img -TargetCfg $TargetCfg
    }
    'Write' {
        $runDir = Get-RunDirectory -RunKey $runKey -Reuse
        $img = Invoke-PhaseDownload -RunDir $runDir -TargetCfg $TargetCfg
        Invoke-PhaseWrite -RunDir $runDir -ImageFile $img -TargetCfg $TargetCfg
    }
    'Restore' {
        $runDir = Get-RunDirectory -RunKey $runKey -Reuse
        Invoke-PhaseRestore -RunDir $runDir -TargetCfg $TargetCfg
    }
    'All' {
        Invoke-PhasePrereqs
        $runDir = Get-RunDirectory -RunKey $runKey
        $img = Invoke-PhaseDownload -RunDir $runDir -TargetCfg $TargetCfg
        Invoke-PhaseReadOnly -RunDir $runDir -TargetCfg $TargetCfg
        Invoke-PhasePlan -RunDir $runDir -ImageFile $img -TargetCfg $TargetCfg
        Write-FlashLog ('run folder: ' + $runDir)
    }
}
