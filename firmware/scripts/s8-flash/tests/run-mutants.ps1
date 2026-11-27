#requires -Version 5.1
Set-StrictMode -Version 2
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$src = Join-Path $root 'StockbotFlash.psm1'
$cliSrc = Join-Path $root 'StockbotFlash.Cli.ps1'
$tempBase = if ($env:TEMP) { $env:TEMP } elseif ($env:TMPDIR) { $env:TMPDIR } else { '/tmp' }
$tmpRoot = Join-Path $tempBase 'stockbot-s8-mutants'
New-Item -ItemType Directory -Path $tmpRoot -Force | Out-Null
Import-Module Pester -RequiredVersion 5.6.1

function Invoke-Mutant {
    param(
        [string]$Name,
        [string]$Pattern,
        [string]$Replacement,
        [hashtable[]]$ExtraReplaces = @(),
        [string[]]$ExpectFail
    )
    $dest = Join-Path $tmpRoot ("mut_" + $Name + '.psm1')
    $text = Get-Content -Raw -LiteralPath $src
    if ($text -notmatch $Pattern) {
        throw ('Mutant {0} pattern does not match the module' -f $Name)
    }
    $text = $text -replace $Pattern, $Replacement
    foreach ($er in $ExtraReplaces) {
        $text = $text -replace $er.Pattern, $er.Replacement
    }
    Set-Content -LiteralPath $dest -Value $text -Encoding UTF8
    $env:STOCKBOT_MODULE_PATH = $dest
    $r = Invoke-Pester -Path (Join-Path $PSScriptRoot '*.Tests.ps1') -PassThru -Quiet
    Remove-Item Env:STOCKBOT_MODULE_PATH -ErrorAction SilentlyContinue
    [pscustomobject]@{
        Mutant = $Name
        Failed = $r.FailedCount
        Passed = $r.PassedCount
        ExpectFail = ($r.FailedCount -gt 0)
    }
}

$mutants = @(
    @{ Name = 'crc_removed'; Pattern = 'if \(-not \$crcOk\) \{ \$invalid = \$true \}'; Replacement = '' ; }
    @{ Name = 'lowest_seq'; Pattern = '-ge \$validSectors\[1\]\.OtaSeq'; Replacement = '-le $validSectors[1].OtaSeq' }
    @{ Name = 'seq_mod'; Pattern = '\(\[int64\]\$activeSeq - 1\) %'; Replacement = '$activeSeq %' }
    @{ Name = 'invalid_skip_removed'; Pattern = 'if \(\$state -eq \$Script:OtaStateInvalid\) \{ \$invalid = \$true \}'; Replacement = '' }
    @{ Name = 'pending_stop_removed'; Pattern = "throw 'STOP: otadata entry in PENDING_VERIFY state \(1\)'"; Replacement = '# pending removed' }
    @{ Name = 'new_stop_removed'; Pattern = "throw 'STOP: otadata entry in NEW state \(0\)'"; Replacement = '# new removed' }
    @{ Name = 'alignment_removed'; Pattern = "throw 'STOP: app offset not aligned to 0x10000'"; Replacement = '# align removed' }
    @{ Name = 'nvs_overlap_removed'; Pattern = '\$isNvs = \(\$p\.Type -eq \$Script:DataType -and \$p\.Subtype -eq \$Script:DataSubtypeNvs\)'; Replacement = '$isNvs = $false' }
    @{ Name = 'otadata_overlap_removed'; Pattern = '\$isOtadata = \(\$p\.Type -eq \$Script:DataType -and \$p\.Subtype -eq \$Script:DataSubtypeOta\)'; Replacement = '$isOtadata = $false' }
    @{ Name = 'chip_id_removed'; Pattern = 'if \(\$chipLe -ne \$Script:Esp32S3ChipIdLe\)'; Replacement = 'if ($false -and $chipLe -ne $Script:Esp32S3ChipIdLe)' }
    @{
        Name = 'outside_boundary'
        Pattern = 'if \(\$i -ge \$SlotOffset -and \$i -lt \$slotPrefixEnd\)'
        Replacement = 'if ($i -ge ($SlotOffset + 1) -and $i -lt $slotPrefixEnd)'
        ExtraReplaces = @(@{ Pattern = 'if \(\$i -ge \$erasedTailEnd -and \$i -lt \$slotEnd\)'; Replacement = 'if ($i -ge $erasedTailEnd -and $i -lt ($slotEnd - 1))' })
    }
    @{ Name = 'confirm_insensitive'; Pattern = 'if \(\$normalized -ceq \$expected\)'; Replacement = 'if ($normalized.ToUpperInvariant() -eq $expected.ToUpperInvariant())' }
    @{ Name = 'monitor_idle_boundary'; Pattern = 'if \(\$idle1\.Count -lt 4 -or \$idle1\.Count -gt 7\)'; Replacement = 'if ($idle1.Count -lt 3 -or $idle1.Count -gt 7)' }
    @{ Name = 'mac_check_removed'; Pattern = "throw 'STOP: device MAC mismatch \(different device on port\)'"; Replacement = 'return $true' }
    @{ Name = 'crc_not_inverted'; Pattern = '\$crc = \[uint64\]\(\[uint32\]\$Initial -bxor \[uint32\]::MaxValue\)'; Replacement = '$crc = [uint64]([uint32]$Initial)' }
    @{ Name = 'aborted_skip_removed'; Pattern = 'if \(\$state -eq \$Script:OtaStateAborted\) \{ \$invalid = \$true \}'; Replacement = '' }
    @{ Name = 'low_flash_guard_weakened'; Pattern = 'if \(\$Offset -lt 0x9000\)'; Replacement = 'if ($Offset -lt 0x1000)' }
    @{ Name = 'overlap_start_only'; Pattern = 'if \(\(\$Offset -lt \$pEnd\) -and \(\$end -gt \$p\.Offset\)\)'; Replacement = 'if (($Offset -ge $p.Offset) -and ($Offset -lt $pEnd))' }
    @{ Name = 'expected_mac_removed'; Pattern = "throw 'STOP: device MAC does not match -ExpectedMac'"; Replacement = 'return $true' }
    @{ Name = 'erased_tail_any_byte'; Pattern = 'if \(\$AfterBytes\[\$i\] -ne 0xFF -and \$BeforeBytes\[\$i\] -ne \$AfterBytes\[\$i\]\)'; Replacement = 'if ($false)' }
    @{ Name = 'erased_tail_whole_slot'; Pattern = 'if \(\$erasedTailEnd -gt \$slotEnd\)'; Replacement = 'if ($true)' }
    @{ Name = 'secure_boot_stop_removed'; Pattern = "throw 'STOP: secure boot is enabled on this device \(espefuse SECURE_BOOT_EN\)'"; Replacement = '# removed' }
    @{ Name = 'flash_crypt_stop_removed'; Pattern = "throw 'STOP: flash encryption is enabled on this device \(espefuse SPI_BOOT_CRYPT_CNT\)'"; Replacement = '# removed' }
    @{ Name = 'crypt_unknown_fails_open'; Pattern = "elseif \(\`$cryptValue -cne 'Enable'\) \{"; Replacement = "elseif (`$false) {" }
    @{ Name = 'secure_not_bool_fails_open'; Pattern = 'if \(-not \(\$secureValue -is \[bool\]\)\) \{'; Replacement = 'if ($false) {' }
    @{ Name = 'board_info_old_mac_regex'; Pattern = "MAC address\\s\*:"; Replacement = 'MAC(?: address)?\s*:' }
    @{ Name = 'flash_size_check_removed'; Pattern = 'if \(\$Identity\.FlashSize -ne \$FlashSize\)'; Replacement = 'if ($false)' }
)

$results = @()
foreach ($m in $mutants) {
    $extra = @()
    if ($m.ContainsKey('ExtraReplaces') -and $m.ExtraReplaces) {
        $extra = @($m.ExtraReplaces)
    }
    $results += Invoke-Mutant -Name $m.Name -Pattern $m.Pattern -Replacement $m.Replacement -ExtraReplaces $extra
}
$results | Format-Table -AutoSize
function Invoke-CliMutant {
    param(
        [string]$Name,
        [string]$Pattern,
        [string]$Replacement
    )
    $dest = Join-Path $tmpRoot ("mutcli_" + $Name + '.Cli.ps1')
    $text = Get-Content -Raw -LiteralPath $cliSrc
    if ($text -notmatch $Pattern) {
        throw ('Mutant {0} pattern does not match the CLI' -f $Name)
    }
    $text = $text -replace $Pattern, $Replacement
    Set-Content -LiteralPath $dest -Value $text -Encoding UTF8
    $env:STOCKBOT_CLI_PATH = $dest
    $r = Invoke-Pester -Path (Join-Path $PSScriptRoot 'Invoke-StockbotFlash.Tests.ps1') -PassThru -Quiet
    Remove-Item Env:STOCKBOT_CLI_PATH -ErrorAction SilentlyContinue
    [pscustomobject]@{
        Mutant = ('cli_' + $Name)
        Failed = $r.FailedCount
        Passed = $r.PassedCount
        ExpectFail = ($r.FailedCount -gt 0)
    }
}

$cliMutants = @(
    @{ Name = 'port_pin_hint'; Pattern = '\$Script:S8_Port = \$found\[0\]'; Replacement = '$Script:S8_Port = $Script:S8_PortHint; return $Script:S8_PortHint' }
    @{ Name = 'ambiguous_port_picks_first'; Pattern = "throw \('STOP: ambiguous COM ports: ' \+ \(\`$found -join ', '\)\)"; Replacement = '$Script:S8_Port = $found[0]; return $found[0]' }
    @{ Name = 'slot_readback_stop_removed'; Pattern = "throw 'STOP: slot read-back prefix mismatch'"; Replacement = '# removed' }
    @{ Name = 'per_call_mac_removed'; Pattern = 'Assert-EspflashResultBoundDevice -Arguments \$Arguments -Result \$result'; Replacement = '# removed' }
    @{ Name = 'live_metadata_removed'; Pattern = 'Assert-LiveMetadataMatchesBackup -RunDir \$RunDir -BackupBytes \$before'; Replacement = '# removed' }
    @{ Name = 'reuse_newest_ignored'; Pattern = 'Sort-Object Name -Descending'; Replacement = 'Sort-Object Name' }
    @{ Name = 'restore_sha_check_removed'; Pattern = "throw 'STOP: backup.bin does not match the sha256 recorded at ReadOnly time'"; Replacement = '# removed' }
    @{ Name = 'efuse_mac_check_removed'; Pattern = 'Test-DeviceMacBinding -ObservedMac \$ef\.Mac'; Replacement = '$null = $ef.Mac; # removed' }
)

foreach ($m in $cliMutants) {
    $results += Invoke-CliMutant -Name $m.Name -Pattern $m.Pattern -Replacement $m.Replacement
}

if (@($results | Where-Object { -not $_.ExpectFail }).Count -gt 0) {
    throw 'Some mutants did not fail tests'
}