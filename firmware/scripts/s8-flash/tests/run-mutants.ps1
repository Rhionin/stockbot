#requires -Version 5.1
Set-StrictMode -Version 2
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$src = Join-Path $root 'StockbotFlash.psm1'
$cliSrc = Join-Path $root 'StockbotFlash.Cli.ps1'
$tempBase = if ($env:TEMP) { $env:TEMP } elseif ($env:TMPDIR) { $env:TMPDIR } else { '/tmp' }
$tmpRoot = Join-Path $tempBase 'stockbot-s8-mutants'
New-Item -ItemType Directory -Path $tmpRoot -Force | Out-Null
$env:STOCKBOT_MODULE_PATH = $null
$env:STOCKBOT_CLI_PATH = $null
Import-Module Pester -RequiredVersion 5.6.1

function Get-FailedTestNames {
    param($PassThruResult)
    return @($PassThruResult.Tests | Where-Object { $_.Result -eq 'Failed' } | ForEach-Object { $_.Name })
}

function Invoke-Mutant {
    param(
        [string]$Name,
        [string]$Pattern,
        [string]$Replacement,
        [hashtable[]]$ExtraReplaces = @(),
        [string[]]$ExpectTests,
        [string]$TestPath = (Join-Path $PSScriptRoot '*.Tests.ps1')
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
    $r = Invoke-Pester -Path $TestPath -PassThru -Quiet
    Remove-Item Env:STOCKBOT_MODULE_PATH -ErrorAction SilentlyContinue
    $failedNames = Get-FailedTestNames -PassThruResult $r
    $matched = $false
    foreach ($exp in $ExpectTests) {
        if (@($failedNames | Where-Object { $_ -like $exp }).Count -gt 0) {
            $matched = $true
            break
        }
    }
    [pscustomobject]@{
        Mutant      = $Name
        Failed      = $r.FailedCount
        Passed      = $r.PassedCount
        ExpectFail  = ($r.FailedCount -gt 0)
        ExpectedHit = $matched
        FailedTests = ($failedNames -join '; ')
    }
}

function Invoke-CliMutant {
    param(
        [string]$Name,
        [string]$Pattern,
        [string]$Replacement,
        [string[]]$ExpectTests
    )
    $dest = Join-Path $tmpRoot ("mutcli_" + $Name + '.Cli.ps1')
    $text = Get-Content -Raw -LiteralPath $cliSrc
    if ($text -notmatch $Pattern) {
        throw ('Mutant {0} pattern does not match the CLI' -f $Name)
    }
    $text = $text -replace $Pattern, $Replacement
    Set-Content -LiteralPath $dest -Value $text -Encoding UTF8
    $env:STOCKBOT_CLI_PATH = $dest
    $r = Invoke-Pester -Path (Join-Path $PSScriptRoot '*.Tests.ps1') -PassThru -Quiet
    Remove-Item Env:STOCKBOT_CLI_PATH -ErrorAction SilentlyContinue
    $failedNames = Get-FailedTestNames -PassThruResult $r
    $matched = $false
    foreach ($exp in $ExpectTests) {
        if (@($failedNames | Where-Object { $_ -like $exp }).Count -gt 0) {
            $matched = $true
            break
        }
    }
    [pscustomobject]@{
        Mutant      = ('cli_' + $Name)
        Failed      = $r.FailedCount
        Passed      = $r.PassedCount
        ExpectFail  = ($r.FailedCount -gt 0)
        ExpectedHit = $matched
        FailedTests = ($failedNames -join '; ')
    }
}

$mutants = @(
    @{ Name = 'crc_removed'; Pattern = 'if \(-not \$crcOk\) \{ \$invalid = \$true \}'; Replacement = ''; ExpectTests = @('*bad CRC*', '*CRC*') }
    @{ Name = 'lowest_seq'; Pattern = '-ge \$validSectors\[1\]\.OtaSeq'; Replacement = '-le $validSectors[1].OtaSeq'; ExpectTests = @('*odd seq*', '*even seq*') }
    @{ Name = 'seq_mod'; Pattern = '\(\[int64\]\$activeSeq - 1\) %'; Replacement = '$activeSeq %'; ExpectTests = @('*odd seq*', '*even seq*') }
    @{ Name = 'invalid_skip_removed'; Pattern = 'if \(\$state -eq \$Script:OtaStateInvalid\) \{ \$invalid = \$true \}'; Replacement = ''; ExpectTests = @('*INVALID*') }
    @{ Name = 'pending_stop_removed'; Pattern = "throw 'STOP: otadata entry in PENDING_VERIFY state \(1\)'"; Replacement = '# pending removed'; ExpectTests = @('*PENDING_VERIFY*') }
    @{ Name = 'new_stop_removed'; Pattern = "throw 'STOP: otadata entry in NEW state \(0\)'"; Replacement = '# new removed'; ExpectTests = @('*NEW state*') }
    @{ Name = 'alignment_removed'; Pattern = "throw 'STOP: app offset not aligned to 0x10000'"; Replacement = '# align removed'; ExpectTests = @('*aligned*') }
    @{ Name = 'nvs_overlap_removed'; Pattern = '\$isNvs = \(\$p\.Type -eq \$Script:DataType -and \$p\.Subtype -eq \$Script:DataSubtypeNvs\)'; Replacement = '$isNvs = $false'; ExpectTests = @('*NVS*') }
    @{ Name = 'otadata_overlap_removed'; Pattern = '\$isOtadata = \(\$p\.Type -eq \$Script:DataType -and \$p\.Subtype -eq \$Script:DataSubtypeOta\)'; Replacement = '$isOtadata = $false'; ExpectTests = @('*otadata*') }
    @{ Name = 'chip_id_removed'; Pattern = 'if \(\$chipLe -ne \$Script:Esp32S3ChipIdLe\)'; Replacement = 'if ($false -and $chipLe -ne $Script:Esp32S3ChipIdLe)'; ExpectTests = @('*chip id*') }
    @{
        Name = 'outside_boundary'
        Pattern = 'if \(\$i -ge \$SlotOffset -and \$i -lt \$slotPrefixEnd\)'
        Replacement = 'if ($i -ge ($SlotOffset + 1) -and $i -lt $slotPrefixEnd)'
        ExtraReplaces = @(@{ Pattern = 'if \(\$i -ge \$erasedTailEnd -and \$i -lt \$slotEnd\)'; Replacement = 'if ($i -ge $erasedTailEnd -and $i -lt ($slotEnd - 1))' })
        ExpectTests = @('*outside slot*', '*padding*')
    }
    @{ Name = 'confirm_insensitive'; Pattern = 'if \(\$normalized -ceq \$expected\)'; Replacement = 'if ($normalized.ToUpperInvariant() -eq $expected.ToUpperInvariant())'; ExpectTests = @('*lowercase sha8*') }
    @{ Name = 'monitor_idle_boundary'; Pattern = 'if \(\$idle1\.Count -lt 4 -or \$idle1\.Count -gt 7\)'; Replacement = 'if ($idle1.Count -lt 3 -or $idle1.Count -gt 7)'; ExpectTests = @('*idle count boundary 3*') }
    @{ Name = 'mac_check_removed'; Pattern = "throw 'STOP: device MAC mismatch \(different device on port\)'"; Replacement = 'return $true'; ExpectTests = @('*MAC mismatch*', '*MAC*') }
    @{ Name = 'crc_not_inverted'; Pattern = '\$crc = \[uint64\]\(\[uint32\]\$Initial -bxor \[uint32\]::MaxValue\)'; Replacement = '$crc = [uint64]([uint32]$Initial)'; ExpectTests = @('*known vectors*', '*CRC matches*') }
    @{ Name = 'aborted_skip_removed'; Pattern = 'if \(\$state -eq \$Script:OtaStateAborted\) \{ \$invalid = \$true \}'; Replacement = ''; ExpectTests = @('*ABORTED*', '*aborted*') }
    @{ Name = 'low_flash_guard_weakened'; Pattern = 'if \(\$Offset -lt 0x9000\)'; Replacement = 'if ($Offset -lt 0x1000)'; ExpectTests = @('*0x9000*', '*low flash*') }
    @{ Name = 'overlap_start_only'; Pattern = 'if \(\(\$Offset -lt \$pEnd\) -and \(\$end -gt \$p\.Offset\)\)'; Replacement = 'if (($Offset -ge $p.Offset) -and ($Offset -lt $pEnd))'; ExpectTests = @('*starts before*', '*overlap*') }
    @{ Name = 'expected_mac_removed'; Pattern = "throw 'STOP: device MAC does not match -ExpectedMac'"; Replacement = 'return $true'; ExpectTests = @('*ExpectedMac*') }
    @{ Name = 'erased_tail_any_byte'; Pattern = 'if \(\$AfterBytes\[\$i\] -ne 0xFF -and \$BeforeBytes\[\$i\] -ne \$AfterBytes\[\$i\]\)'; Replacement = 'if ($false)'; ExpectTests = @('*erased tail*') }
    @{ Name = 'erased_tail_whole_slot'; Pattern = 'if \(\$erasedTailEnd -gt \$slotEnd\)'; Replacement = 'if ($true)'; ExpectTests = @('*erased tail*', '*passes the erased-tail*') }
    @{ Name = 'secure_boot_stop_removed'; Pattern = "throw 'STOP: secure boot is enabled on this device \(espefuse SECURE_BOOT_EN\)'"; Replacement = '# removed'; ExpectTests = @('*secure boot*') }
    @{ Name = 'flash_crypt_stop_removed'; Pattern = "throw 'STOP: flash encryption is enabled on this device \(espefuse SPI_BOOT_CRYPT_CNT\)'"; Replacement = '# removed'; ExpectTests = @('*flash encryption*') }
    @{ Name = 'crypt_unknown_fails_open'; Pattern = "elseif \(\`$cryptValue -cne 'Enable'\) \{"; Replacement = "elseif (`$false) {"; ExpectTests = @('*SPI_BOOT_CRYPT_CNT*') }
    @{ Name = 'secure_not_bool_fails_open'; Pattern = 'if \(-not \(\$secureValue -is \[bool\]\)\) \{'; Replacement = 'if ($false) {'; ExpectTests = @('*SECURE_BOOT_EN*') }
    @{ Name = 'board_info_old_mac_regex'; Pattern = '(?i)\^\\s\*MAC address\\s\*:'; Replacement = '(?i)MAC(?: address)?\s*:'; ExpectTests = @('*MAC address*') }
    @{ Name = 'flash_size_check_removed'; Pattern = 'if \(\$Identity\.FlashSize -ne \$FlashSize\)'; Replacement = 'if ($false)'; ExpectTests = @('*flash size*') }
    @{ Name = 'boardinfo_security_open'; Pattern = "throw 'STOP: secure boot or flash encryption enabled \(board-info\)'"; Replacement = '# removed'; ExpectTests = @('*board-info*', '*secure boot*') }
)

$cliMutants = @(
    @{ Name = 'port_pin_hint'; Pattern = '\$Script:S8_Port = \$found\[0\]'; Replacement = '$Script:S8_Port = $Script:S8_PortHint; return $Script:S8_PortHint'; ExpectTests = @('*ambiguous*', '*COM*') }
    @{ Name = 'ambiguous_port_picks_first'; Pattern = "throw \('STOP: ambiguous COM ports: ' \+ \(\`$found -join ', '\)\)"; Replacement = '$Script:S8_Port = $found[0]; return $found[0]'; ExpectTests = @('*ambiguous*') }
    @{ Name = 'slot_readback_stop_removed'; Pattern = "throw 'STOP: slot read-back prefix mismatch'"; Replacement = '# removed'; ExpectTests = @('*read-back*') }
    @{ Name = 'per_call_mac_removed'; Pattern = 'Assert-EspflashResultBoundDevice -Arguments \$Arguments -Result \$result'; Replacement = '# removed'; ExpectTests = @('*MAC*') }
    @{ Name = 'live_metadata_removed'; Pattern = 'Assert-LiveMetadataMatchesBackup -RunDir \$RunDir -BackupBytes \$before'; Replacement = '# removed'; ExpectTests = @('*changed since the ReadOnly*') }
    @{ Name = 'reuse_newest_ignored'; Pattern = 'Sort-Object Name -Descending'; Replacement = 'Sort-Object Name'; ExpectTests = @('*reuse the newest*') }
    @{ Name = 'restore_sha_check_removed'; Pattern = "throw 'STOP: backup.bin does not match the sha256 recorded at ReadOnly time'"; Replacement = '# removed'; ExpectTests = @('*does not match the sha256*') }
    @{ Name = 'efuse_mac_check_removed'; Pattern = 'Test-DeviceMacBinding -ObservedMac \$ef\.Mac'; Replacement = '$null = $ef.Mac; # removed'; ExpectTests = @('*espefuse reports a different MAC*') }
    @{ Name = 'write_efuse_gate_removed'; Pattern = "throw 'STOP: eFuse check not passed'"; Replacement = '# removed'; ExpectTests = @('*EfuseOk*', '*eFuse check not passed*') }
    @{ Name = 'write_backup_md5_gate_removed'; Pattern = "throw 'STOP: verified backup MD5 missing'"; Replacement = '# removed'; ExpectTests = @('*BackupMd5*', '*backup MD5 missing*') }
    @{ Name = 'write_table_gate_removed'; Pattern = "throw 'STOP: partition table not in allow-list'"; Replacement = '# removed'; ExpectTests = @('*TableSha256*', '*allow-list*') }
    @{ Name = 'write_plan_gate_removed'; Pattern = "throw 'STOP: Plan required before Write'"; Replacement = '# removed'; ExpectTests = @('*PlanComplete*', '*Plan required*') }
    @{ Name = 'monitor_fail_stop_removed'; Pattern = "throw 'STOP: monitor verdict FAIL'"; Replacement = '# removed'; ExpectTests = @('*monitor verdict FAIL*') }
    @{ Name = 'write_confirm_gate_removed'; Pattern = "throw 'STOP: typed confirmation required'"; Replacement = '# removed'; ExpectTests = @('*confirmation required*') }
    @{ Name = 'restore_identity_removed'; Pattern = '\$null = Invoke-PhaseDeviceIdentityCheck -TargetCfg \$TargetCfg -State \$state'; Replacement = '# removed'; ExpectTests = @('*Restore STOP on MAC*') }
    @{ Name = 'write_range_call_removed'; Pattern = 'Test-WriteRangeAllowed -Offset \$offset -Length \$image\.Length -PartitionEntries \$entries'; Replacement = '# removed'; ExpectTests = @('*0x9000*', '*0xF000*', '*overlap*') }
)

$results = @()
foreach ($m in $mutants) {
    $extra = @()
    if ($m.ContainsKey('ExtraReplaces') -and $m.ExtraReplaces) {
        $extra = @($m.ExtraReplaces)
    }
    $results += Invoke-Mutant -Name $m.Name -Pattern $m.Pattern -Replacement $m.Replacement -ExtraReplaces $extra -ExpectTests $m.ExpectTests
}
foreach ($m in $cliMutants) {
    $results += Invoke-CliMutant -Name $m.Name -Pattern $m.Pattern -Replacement $m.Replacement -ExpectTests $m.ExpectTests
}

$results | Format-Table -AutoSize Mutant, Failed, ExpectFail, ExpectedHit, FailedTests

$bad = @($results | Where-Object { -not $_.ExpectFail -or -not $_.ExpectedHit })
if ($bad.Count -gt 0) {
  throw ('Mutants not killed by an expected test: ' + (($bad | ForEach-Object { $_.Mutant }) -join ', '))
}