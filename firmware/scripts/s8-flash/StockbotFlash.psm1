#requires -Version 5.1
<#
.SYNOPSIS
    Pure helpers for Stockbot StickS3 (slice S8) flash planning — no device I/O.

.DESCRIPTION
    Parsing partition tables, otadata (ESP-IDF v5.3.x CRC), image headers, typed
    confirmation, and write-range guards. Never flashes hardware from this module.

.NOTES
    Otadata CRC matches bootloader_common_ota_select_crc() in ESP-IDF v5.3.2
    (components/bootloader_support/src/bootloader_common_loader.c): esp_rom_crc32_le
    with init 0xFFFFFFFF over the 4-byte ota_seq field only. esp_rom_crc32_le inverts
    the CRC on entry and on exit (components/esp_rom/linux/esp_rom_crc.c), so the
    result equals zlib.crc32(ota_seq, 0xFFFFFFFF).
#>
Set-StrictMode -Version 2

$Script:PartitionEntryMagic = [byte[]](0xAA, 0x50)
$Script:Md5PartitionMagic = [byte[]](0xEB, 0xEB)
$Script:OtadataSectorSize = 0x1000
$Script:OtadataEntrySize = 32
$Script:FlashEraseSectorSize = 0x1000

$Script:OtaStateNew = 0
$Script:OtaStatePendingVerify = 1
$Script:OtaStateValid = 2
$Script:OtaStateInvalid = 3
$Script:OtaStateAborted = 4
$Script:OtaStateUndefined = [uint32]::MaxValue

$Script:AppType = 0
$Script:DataType = 1
$Script:DataSubtypeOta = 0
$Script:DataSubtypeNvs = 2

$Script:ImageMagic = 0xE9
$Script:Esp32S3ChipIdLe = 0x0009
$Script:AppDescriptorMagic = 0xABCD5432

function Get-EspRomCrc32Le {
    param(
        [Parameter(Mandatory)]
        [uint32]$Initial,
        [Parameter(Mandatory)]
        [byte[]]$Data
    )
    $poly = [uint64]3988292384  # 0xEDB88320
    $crc = [uint64]([uint32]$Initial -bxor [uint32]::MaxValue)
    foreach ($b in $Data) {
        $crc = ($crc -bxor [uint64]$b) -band 0xFFFFFFFF
        for ($bit = 0; $bit -lt 8; $bit++) {
            if (($crc -band 1) -ne 0) {
                $crc = ((($crc -shr 1) -bxor $poly) -band 0xFFFFFFFF)
            }
            else {
                $crc = ($crc -shr 1) -band 0xFFFFFFFF
            }
        }
    }
    return [uint32]([uint64]$crc -bxor [uint64][uint32]::MaxValue)
}

function Get-OtadataEntryCrc {
    param([byte[]]$EntryBytes)
    if ($EntryBytes.Length -lt 4) {
        throw 'Otadata entry too short for CRC'
    }
    $seqBytes = $EntryBytes[0..3]
    return Get-EspRomCrc32Le -Initial ([uint32]::MaxValue) -Data $seqBytes
}

function Read-PartitionTable {
    [CmdletBinding()]
  param(
        [Parameter(Mandatory)]
        [byte[]]$TableBytes
    )
    if ($null -eq $TableBytes -or $TableBytes.Length -lt 32) {
        throw 'Partition table must be at least 32 bytes'
    }
    $entries = New-Object System.Collections.ArrayList
    $pos = 0
    $len = $TableBytes.Length
    while ($pos + 32 -le $len) {
        $chunk = $TableBytes[$pos..($pos + 31)]
        $pos += 32
        if (($chunk[0] -eq 0xFF) -and ($chunk[1] -eq 0xFF)) {
            break
        }
        if (($chunk[0] -eq $Script:Md5PartitionMagic[0]) -and ($chunk[1] -eq $Script:Md5PartitionMagic[1])) {
            continue
        }
        if (($chunk[0] -ne $Script:PartitionEntryMagic[0]) -or ($chunk[1] -ne $Script:PartitionEntryMagic[1])) {
            throw ('Invalid partition entry magic at offset {0}' -f ($pos - 32))
        }
        $ptype = $chunk[2]
        $subtype = $chunk[3]
        $offset = [BitConverter]::ToUInt32($chunk, 4)
        $size = [BitConverter]::ToUInt32($chunk, 8)
        $nameBytes = $chunk[12..27]
        $name = ([System.Text.Encoding]::ASCII.GetString($nameBytes)).Trim([char]0)
        $flags = [BitConverter]::ToUInt32($chunk, 28)
        [void]$entries.Add([pscustomobject]@{
            Name    = $name
            Type    = $ptype
            Subtype = $subtype
            Offset  = $offset
            Size    = $size
            Flags   = $flags
        })
    }
    if ($entries.Count -eq 0) {
        throw 'Partition table has no entries'
    }
    return @($entries.ToArray())
}

function Read-Otadata {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [byte[]]$OtadataBytes
    )
    $sectors = @()
    $sectorCount = [int]($OtadataBytes.Length / $Script:OtadataSectorSize)
    if ($sectorCount -lt 1) {
        throw 'Otadata region too small'
    }
    for ($s = 0; $s -lt 2 -and ($s * $Script:OtadataSectorSize) -lt $OtadataBytes.Length; $s++) {
        $base = $s * $Script:OtadataSectorSize
        $entryBytes = $OtadataBytes[$base..($base + $Script:OtadataEntrySize - 1)]
        $seq = [BitConverter]::ToUInt32($entryBytes, 0)
        $label = [System.Text.Encoding]::ASCII.GetString($entryBytes, 4, 20).Trim([char]0)
        $state = [BitConverter]::ToUInt32($entryBytes, 24)
        $storedCrc = [BitConverter]::ToUInt32($entryBytes, 28)
        $computedCrc = Get-OtadataEntryCrc -EntryBytes $entryBytes
        $crcOk = ($computedCrc -eq $storedCrc)
        $invalid = $false
        if ($seq -eq [uint32]::MaxValue) { $invalid = $true }
        if ($state -eq $Script:OtaStateInvalid) { $invalid = $true }
        if ($state -eq $Script:OtaStateAborted) { $invalid = $true }
        if (-not $crcOk) { $invalid = $true }
        $valid = -not $invalid
        $sectors += [pscustomobject]@{
            SectorIndex = $s
            OtaSeq      = $seq
            Label       = $label
            OtaState    = $state
            StoredCrc   = $storedCrc
            ComputedCrc = $computedCrc
            CrcValid    = $crcOk
            Valid       = $valid
        }
    }
    return @($sectors)
}

function Select-AppSlot {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        $PartitionEntries,
        [Parameter(Mandatory)]
        $OtadataSectors
    )
    $otaSlots = @($PartitionEntries | Where-Object {
            $_.Type -eq $Script:AppType -and $_.Subtype -ge 0x10 -and $_.Subtype -le 0x1F
        } | Sort-Object Offset)
    if ($otaSlots.Count -eq 0) {
        throw 'STOP: no OTA app slots in partition table'
    }
    $slotCount = $otaSlots.Count
    foreach ($sec in $OtadataSectors) {
        if ($sec.Valid) {
            if ($sec.OtaState -eq $Script:OtaStateNew) {
                throw 'STOP: otadata entry in NEW state (0)'
            }
            if ($sec.OtaState -eq $Script:OtaStatePendingVerify) {
                throw 'STOP: otadata entry in PENDING_VERIFY state (1)'
            }
        }
    }
    $validSectors = @($OtadataSectors | Where-Object { $_.Valid })
    $activeIndex = -1
    $activeSeq = [uint32]0
    if ($validSectors.Count -eq 0) {
        $factory = @($PartitionEntries | Where-Object {
                $_.Type -eq $Script:AppType -and $_.Subtype -eq 0
            })
        if ($factory.Count -gt 0) {
            return [pscustomobject]@{
                SlotName   = $factory[0].Name
                Offset     = $factory[0].Offset
                Size       = $factory[0].Size
                OtaSeq     = 0
                Reason     = 'no valid otadata; factory partition'
                OtadataIdx = -1
            }
        }
        return [pscustomobject]@{
            SlotName   = $otaSlots[0].Name
            Offset     = $otaSlots[0].Offset
            Size       = $otaSlots[0].Size
            OtaSeq     = 0
            Reason     = 'no valid otadata; first OTA slot'
            OtadataIdx = -1
        }
    }
    if ($validSectors.Count -eq 1) {
        $activeIndex = $validSectors[0].SectorIndex
        $activeSeq = $validSectors[0].OtaSeq
    }
    else {
        if ($validSectors[0].OtaSeq -ge $validSectors[1].OtaSeq) {
            $activeIndex = $validSectors[0].SectorIndex
            $activeSeq = $validSectors[0].OtaSeq
        }
        else {
            $activeIndex = $validSectors[1].SectorIndex
            $activeSeq = $validSectors[1].OtaSeq
        }
    }
    $slotIdx = ([int64]$activeSeq - 1) % $slotCount
    if ($slotIdx -lt 0) { $slotIdx = 0 }
    $chosen = $otaSlots[$slotIdx]
    return [pscustomobject]@{
        SlotName   = $chosen.Name
        Offset     = $chosen.Offset
        Size       = $chosen.Size
        OtaSeq     = $activeSeq
        Reason     = 'otadata selection'
        OtadataIdx = $activeIndex
    }
}

function Test-ImageHeader {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [byte[]]$ImageBytes,
        [int]$SlotSize = 0,
        [switch]$QuietWarning
    )
    $result = [pscustomobject]@{
        Ok       = $true
        Errors   = New-Object System.Collections.ArrayList
        Warnings = New-Object System.Collections.ArrayList
    }
    if ($ImageBytes.Length -lt 16) {
        [void]$result.Errors.Add('Image too short')
        $result.Ok = $false
        return $result
    }
    if ($ImageBytes[0] -eq 0x7F -and $ImageBytes[1] -eq 0x45 -and $ImageBytes[2] -eq 0x4C -and $ImageBytes[3] -eq 0x46) {
        [void]$result.Errors.Add('ELF image rejected (use firmware.bin app image)')
        $result.Ok = $false
        return $result
    }
    if ($ImageBytes[0] -ne $Script:ImageMagic) {
        [void]$result.Errors.Add('Invalid image magic (expected 0xE9)')
        $result.Ok = $false
        return $result
    }
    if ($ImageBytes.Length -ge 14) {
        $chipLe = [BitConverter]::ToUInt16($ImageBytes, 12)
        if ($chipLe -ne $Script:Esp32S3ChipIdLe) {
            [void]$result.Errors.Add(('Chip id 0x{0:X4} is not ESP32-S3 (0x0009)' -f $chipLe))
            $result.Ok = $false
        }
    }
    if ($ImageBytes.Length -ge 0x8002) {
        if (($ImageBytes[0] -eq $Script:ImageMagic) -and ($ImageBytes[0x8000] -eq 0xAA) -and ($ImageBytes[0x8001] -eq 0x50)) {
            [void]$result.Errors.Add('Merged/factory image rejected (partition table at 0x8000)')
            $result.Ok = $false
        }
    }
    if ($SlotSize -gt 0 -and $ImageBytes.Length -gt $SlotSize) {
        [void]$result.Errors.Add('Image larger than OTA slot')
        $result.Ok = $false
    }
    if ($ImageBytes.Length -ge 36) {
        $desc = [BitConverter]::ToUInt32($ImageBytes, 32)
        if ($desc -ne $Script:AppDescriptorMagic) {
            if (-not $QuietWarning) {
                [void]$result.Warnings.Add('App descriptor magic 0xABCD5432 not found at offset 0x20 (warning only)')
            }
        }
    }
    return $result
}

function Get-BytesSha256Hex {
    param([byte[]]$Bytes)
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        $hash = $sha.ComputeHash($Bytes)
    }
    finally {
        $sha.Dispose()
    }
    return ([BitConverter]::ToString($hash)).Replace('-', '').ToLowerInvariant()
}

function Test-TableAllowed {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [byte[]]$TableBytes,
        [Parameter(Mandatory)]
        [string[]]$AllowedSha256
    )
    $sha = Get-BytesSha256Hex -Bytes $TableBytes
    foreach ($allow in $AllowedSha256) {
        if ($sha -eq $allow.ToLowerInvariant()) {
            return [pscustomobject]@{ Allowed = $true; Sha256 = $sha }
        }
    }
    throw ('STOP: partition table sha256 {0} is not in the allowed list' -f $sha)
}

function Test-WriteRangeAllowed {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [uint32]$Offset,
        [Parameter(Mandatory)]
        [uint32]$Length,
        [Parameter(Mandatory)]
        $PartitionEntries
    )
    $end = [uint64]$Offset + [uint64]$Length
    if ($Offset -lt 0x9000) {
        throw 'STOP: write range overlaps protected low flash (bootloader/table below 0x9000)'
    }
    foreach ($p in $PartitionEntries) {
        $pEnd = [uint64]$p.Offset + [uint64]$p.Size
        $isNvs = ($p.Type -eq $Script:DataType -and $p.Subtype -eq $Script:DataSubtypeNvs)
        $isOtadata = ($p.Type -eq $Script:DataType -and $p.Subtype -eq $Script:DataSubtypeOta)
        if ($isNvs -or $isOtadata) {
            if (($Offset -lt $pEnd) -and ($end -gt $p.Offset)) {
                throw ('STOP: write range overlaps protected partition {0}' -f $p.Name)
            }
        }
    }
}

function Test-FlashPlanConstraints {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [uint32]$AppOffset,
        [Parameter(Mandatory)]
        [int]$ImageLength,
        [Parameter(Mandatory)]
        [uint32]$SlotSize,
        [Parameter(Mandatory)]
        $PartitionEntries
    )
    if (($AppOffset % 0x10000) -ne 0) {
        throw 'STOP: app offset not aligned to 0x10000'
    }
    if ($ImageLength -gt $SlotSize) {
        throw 'STOP: image larger than slot'
    }
    $imgEnd = [uint64]$AppOffset + [uint64]$ImageLength
    $slotEnd = [uint64]$AppOffset + [uint64]$SlotSize
    if ($imgEnd -gt $slotEnd) {
        throw 'STOP: image extends past slot end'
    }
    Test-WriteRangeAllowed -Offset $AppOffset -Length $ImageLength -PartitionEntries $PartitionEntries
}

function Test-OutsideSlotUnchanged {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [byte[]]$BeforeBytes,
        [Parameter(Mandatory)]
        [byte[]]$AfterBytes,
        [Parameter(Mandatory)]
        [uint32]$SlotOffset,
        [Parameter(Mandatory)]
        [int]$ImageLength,
        [Parameter(Mandatory)]
        [uint32]$SlotSize
    )
    if ($BeforeBytes.Length -ne $AfterBytes.Length) {
        throw 'Flash dumps differ in length'
    }
    $len = $BeforeBytes.Length
    $slotPrefixEnd = [int64]$SlotOffset + [int64]$ImageLength
    $slotEnd = [int64]$SlotOffset + [int64]$SlotSize
    $sector = [int64]$Script:FlashEraseSectorSize
    $erasedTailEnd = [int64]([Math]::Ceiling($slotPrefixEnd / $sector)) * $sector
    if ($erasedTailEnd -gt $slotEnd) {
        $erasedTailEnd = $slotEnd
    }
    for ($i = 0; $i -lt $len; $i++) {
        if ($i -ge $SlotOffset -and $i -lt $slotPrefixEnd) {
            continue
        }
        if ($i -ge $slotPrefixEnd -and $i -lt $erasedTailEnd) {
            if ($AfterBytes[$i] -ne 0xFF -and $BeforeBytes[$i] -ne $AfterBytes[$i]) {
                throw ('STOP: erased tail of the last written sector changed at offset 0x{0:X} (expected 0xFF or the old byte)' -f $i)
            }
            continue
        }
        if ($i -ge $erasedTailEnd -and $i -lt $slotEnd) {
            if ($BeforeBytes[$i] -ne $AfterBytes[$i]) {
                throw ('STOP: inside-slot padding changed at offset 0x{0:X}' -f $i)
            }
            continue
        }
        if ($BeforeBytes[$i] -ne $AfterBytes[$i]) {
            throw ('Outside-slot byte changed at offset 0x{0:X}' -f $i)
        }
    }
    return $true
}

function Test-TypedConfirmation {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string]$UserInput,
        [Parameter(Mandatory)]
        [string]$ExpectedOffsetHex,
        [Parameter(Mandatory)]
        [string]$ImageSha256Hex
    )
    if ([string]::IsNullOrWhiteSpace($UserInput)) {
        return $false
    }
    $normalized = ($UserInput.Trim() -replace '\s+', ' ')
    $sha8 = $ImageSha256Hex.Substring(0, 8).ToUpperInvariant()
    $off = $ExpectedOffsetHex.Trim().ToLowerInvariant()
    if ($off.StartsWith('0x')) {
        $off = '0x' + $off.Substring(2).ToUpperInvariant()
    }
    else {
        $off = $off.ToUpperInvariant()
    }
    $expected = ('WRITE {0} {1}' -f $off, $sha8)
    if ($normalized -ceq $expected) {
        return $true
    }
    return $false
}

function Test-TypedRestoreConfirmation {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string]$UserInput,
        [Parameter(Mandatory)]
        [string]$ExpectedOffsetHex,
        [Parameter(Mandatory)]
        [string]$SlotSha256Hex
    )
    if ([string]::IsNullOrWhiteSpace($UserInput)) {
        return $false
    }
    $normalized = ($UserInput.Trim() -replace '\s+', ' ')
    $sha8 = $SlotSha256Hex.Substring(0, 8).ToUpperInvariant()
    $off = $ExpectedOffsetHex.Trim().ToLowerInvariant()
    if ($off.StartsWith('0x')) {
        $off = '0x' + $off.Substring(2).ToUpperInvariant()
    }
    else {
        $off = $off.ToUpperInvariant()
    }
    $expected = ('RESTORE {0} {1}' -f $off, $sha8)
    return ($normalized -ceq $expected)
}

function Get-MonitorVerdict {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [AllowEmptyString()]
        [string]$Capture1Text,
        [Parameter(Mandatory)]
        [AllowEmptyString()]
        [string]$Capture2Text,
        [Parameter(Mandatory)]
        [string]$TargetId
    )
    $bootNeedle = 'stockbot-scanner-esp boot'
    $idleNeedle = 'stockbot-scanner-esp idle'
    $targetToken = 'target=' + $TargetId
    function Get-CaptureMarkerLines {
        param(
            [string]$Text,
            [string]$Kind
        )
        $out = New-Object System.Collections.ArrayList
        foreach ($raw in ($Text -split "`r?`n")) {
            $line = $raw.Trim()
            if ([string]::IsNullOrWhiteSpace($line)) {
                continue
            }
            if ($line -notlike ('*' + $targetToken + '*')) {
                continue
            }
            if ($Kind -eq 'boot' -and $line -like ('*' + $bootNeedle + '*')) {
                [void]$out.Add($line)
            }
            if ($Kind -eq 'idle' -and $line -like ('*' + $idleNeedle + '*')) {
                [void]$out.Add($line)
            }
        }
        return @($out.ToArray())
    }
    if ([string]::IsNullOrEmpty($Capture1Text) -or [string]::IsNullOrEmpty($Capture2Text)) {
        return [pscustomobject]@{
            Pass              = $false
            Reason            = 'empty capture'
            MarkerLines       = @()
            BootCountCapture1 = 0
            IdleCountCapture1 = 0
            BootCountCapture2 = 0
        }
    }
    $boot1 = @(Get-CaptureMarkerLines -Text $Capture1Text -Kind 'boot')
    $idle1 = @(Get-CaptureMarkerLines -Text $Capture1Text -Kind 'idle')
    $boot2 = @(Get-CaptureMarkerLines -Text $Capture2Text -Kind 'boot')
    $reasons = New-Object System.Collections.ArrayList
    $pass = $true
    if ($boot1.Count -ne 1) {
        $pass = $false
        [void]$reasons.Add(('capture1 boot lines expected 1 got {0}' -f $boot1.Count))
    }
    if ($idle1.Count -lt 4 -or $idle1.Count -gt 7) {
        $pass = $false
        [void]$reasons.Add(('capture1 idle lines expected 4-7 got {0}' -f $idle1.Count))
    }
    if ($boot2.Count -lt 1) {
        $pass = $false
        [void]$reasons.Add('capture2 missing boot line')
    }
    $markerLines = New-Object System.Collections.ArrayList
    foreach ($l in $boot1) { [void]$markerLines.Add($l) }
    foreach ($l in $idle1) { [void]$markerLines.Add($l) }
    foreach ($l in $boot2) { [void]$markerLines.Add($l) }
    return [pscustomobject]@{
        Pass              = $pass
        Reason            = ($reasons -join '; ')
        MarkerLines       = @($markerLines.ToArray())
        BootCountCapture1 = $boot1.Count
        IdleCountCapture1 = $idle1.Count
        BootCountCapture2 = $boot2.Count
    }
}

function Get-ConnectAttempts {
    [CmdletBinding()]
    param()
    return @(
        [pscustomobject]@{ Name = 'usb-reset'; BeforeArg = 'usb-reset' },
        [pscustomobject]@{ Name = 'default-reset'; BeforeArg = 'default-reset' },
        [pscustomobject]@{ Name = 'no-reset'; BeforeArg = 'no-reset' }
    )
}

function Get-DownloadModeInstructionText {
    [CmdletBinding()]
    param()
    return @(
        'Manual download mode required:',
        'Hold the power button until the green LED blinks, then connect a USB-C data cable.',
        'Wait for the serial port to appear and re-run.'
    ) -join [Environment]::NewLine
}

function Parse-BoardInfoIdentity {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [AllowEmptyString()]
        [string]$Text
    )
    if ([string]::IsNullOrWhiteSpace($Text)) {
        throw 'STOP: espflash printed no board info'
    }
    $mac = $null
    $chip = $null
    $flashBytes = $null
    foreach ($line in ($Text -split "`r?`n")) {
        if ($line -match '(?i)^\s*MAC address\s*:\s*([0-9a-f]{2}(?::[0-9a-f]{2}){5})\s*$') {
            $mac = $Matches[1].ToLowerInvariant()
        }
        if ($line -match '(?i)^\s*Chip type\s*:\s*(\S+)') {
            $chip = $Matches[1].ToLowerInvariant()
        }
        if ($line -match '(?i)^\s*Flash size\s*:\s*(\d+)\s*(KB|MB)\s*$') {
            $mult = 1024
            if ($Matches[2].ToUpperInvariant() -eq 'MB') {
                $mult = 1024 * 1024
            }
            $flashBytes = [int64]$Matches[1] * $mult
        }
    }
    if (-not $mac) {
        throw 'STOP: espflash board info has no "MAC address:" line'
    }
    if (-not $chip) {
        throw 'STOP: espflash board info has no "Chip type:" line'
    }
    return [pscustomobject]@{
        Mac       = $mac
        Chip      = $chip
        FlashSize = $flashBytes
    }
}

function ConvertTo-StrictSecurityFlag {
    param([string]$Value, [string]$Field)
    $v = ([string]$Value).Trim().ToLowerInvariant()
    if (@('false', 'disabled', 'disable', '0') -contains $v) { return $false }
    if (@('true', 'enabled', 'enable', '1') -contains $v) { return $true }
    throw ('STOP: unrecognised value for ' + $Field + ' (fail closed)')
}

function Test-BoardInfoSecurityAllowed {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [AllowEmptyString()]
        [string]$Text
    )
    $secure = $null
    $flashEnc = $null
    $cryptCntRaw = $null
    foreach ($line in ($Text -split "`r?`n")) {
        if ($line -match '(?i)Secure Boot\s*:\s*(\S+)') {
            $secure = ConvertTo-StrictSecurityFlag -Value $Matches[1] -Field 'Secure Boot'
        }
        if ($line -match '(?i)Flash Encryption\s*:\s*(\S+)') {
            $flashEnc = ConvertTo-StrictSecurityFlag -Value $Matches[1] -Field 'Flash Encryption'
        }
        if ($line -match '(?i)SPI_BOOT_CRYPT_CNT\s*:\s*(\S+)') {
            $cryptCntRaw = $Matches[1]
        }
    }
    if ($null -eq $secure -and $null -eq $flashEnc -and $null -eq $cryptCntRaw) {
        return $true
    }
    if ($secure) {
        throw 'STOP: secure boot or flash encryption enabled (board-info)'
    }
    if ($flashEnc) {
        throw 'STOP: secure boot or flash encryption enabled (board-info)'
    }
    if ($cryptCntRaw) {
        $bits = [uint32]0
        if ($cryptCntRaw -match '^0x[0-9a-fA-F]+$') {
            $bits = [Convert]::ToUInt32($cryptCntRaw.Substring(2), 16)
        }
        elseif ($cryptCntRaw -match '^\d+$') {
            $bits = [uint32][int]$cryptCntRaw
        }
        else {
            throw 'STOP: unrecognised SPI_BOOT_CRYPT_CNT value (fail closed)'
        }
        if (($bits -band [uint32]7) -ne 0) {
            throw 'STOP: SPI_BOOT_CRYPT_CNT nonzero (board-info)'
        }
    }
    return $true
}

function Test-BoardIdentityMatchesTarget {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        $Identity,
        [Parameter(Mandatory)]
        [string]$Chip,
        [Parameter(Mandatory)]
        [int64]$FlashSize
    )
    if ($Identity.Chip -ne $Chip.ToLowerInvariant()) {
        throw ('STOP: device chip is {0}, expected {1}' -f $Identity.Chip, $Chip)
    }
    if ($null -eq $Identity.FlashSize) {
        throw 'STOP: espflash board info has no "Flash size:" line'
    }
    if ($Identity.FlashSize -ne $FlashSize) {
        throw ('STOP: device flash size is {0} bytes, expected {1}' -f $Identity.FlashSize, $FlashSize)
    }
    return $true
}

function Test-DeviceMacBinding {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string]$ObservedMac,
        [string]$BoundMac,
        [string]$ExpectedMac
    )
    $obs = $ObservedMac.ToLowerInvariant()
    if ($ExpectedMac) {
        $exp = $ExpectedMac.ToLowerInvariant()
        if ($obs -ne $exp) {
            throw 'STOP: device MAC does not match -ExpectedMac'
        }
    }
    if ($BoundMac) {
        $bound = $BoundMac.ToLowerInvariant()
        if ($obs -ne $bound) {
            throw 'STOP: device MAC mismatch (different device on port)'
        }
    }
    return $true
}

function Parse-EfuseSecuritySummary {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [AllowEmptyString()]
        [string]$SummaryJson
    )
    if ([string]::IsNullOrWhiteSpace($SummaryJson)) {
        throw 'STOP: espefuse summary is empty'
    }
    try {
        $doc = $SummaryJson | ConvertFrom-Json
    }
    catch {
        throw 'STOP: espefuse summary is not valid JSON'
    }
    $names = @($doc.PSObject.Properties | ForEach-Object { $_.Name })
    foreach ($field in @('SECURE_BOOT_EN', 'SPI_BOOT_CRYPT_CNT')) {
        if ($names -notcontains $field) {
            throw ('STOP: espefuse summary has no {0} field' -f $field)
        }
        $entry = $doc.$field
        if ($null -eq $entry -or @($entry.PSObject.Properties | ForEach-Object { $_.Name }) -notcontains 'value') {
            throw ('STOP: espefuse summary field {0} has no value' -f $field)
        }
    }
    $secureValue = $doc.SECURE_BOOT_EN.value
    if (-not ($secureValue -is [bool])) {
        throw ('STOP: espefuse SECURE_BOOT_EN value "{0}" is not true or false' -f $secureValue)
    }
    $cryptValue = $doc.SPI_BOOT_CRYPT_CNT.value
    $flashCrypt = $true
    if ($cryptValue -is [string]) {
        if ($cryptValue -ceq 'Disable') {
            $flashCrypt = $false
        }
        elseif ($cryptValue -cne 'Enable') {
            throw ('STOP: espefuse SPI_BOOT_CRYPT_CNT value "{0}" is not Disable or Enable' -f $cryptValue)
        }
    }
    elseif (($cryptValue -is [int]) -or ($cryptValue -is [long])) {
        $flashCrypt = ([int64]$cryptValue -ne 0)
    }
    else {
        throw ('STOP: espefuse SPI_BOOT_CRYPT_CNT value "{0}" is not recognised' -f $cryptValue)
    }
    $mac = $null
    if ($names -contains 'MAC') {
        $macText = [string]$doc.MAC.value
        if ($macText -match '(?i)^\s*([0-9a-f]{2}(?::[0-9a-f]{2}){5})') {
            $mac = $Matches[1].ToLowerInvariant()
        }
    }
    if ($secureValue) {
        throw 'STOP: secure boot is enabled on this device (espefuse SECURE_BOOT_EN)'
    }
    if ($flashCrypt) {
        throw 'STOP: flash encryption is enabled on this device (espefuse SPI_BOOT_CRYPT_CNT)'
    }
    return [pscustomobject]@{
        SecureBootEnabled = $false
        FlashEncryption   = $false
        Mac               = $mac
        Allowed           = $true
    }
}

Export-ModuleMember -Function @(
    'Get-ConnectAttempts',
    'Get-DownloadModeInstructionText',
    'Parse-BoardInfoIdentity',
    'Test-BoardInfoSecurityAllowed',
    'Test-BoardIdentityMatchesTarget',
    'Test-DeviceMacBinding',
    'Get-EspRomCrc32Le',
    'Get-OtadataEntryCrc',
    'Read-PartitionTable',
    'Read-Otadata',
    'Select-AppSlot',
    'Test-ImageHeader',
    'Get-BytesSha256Hex',
    'Test-TableAllowed',
    'Test-WriteRangeAllowed',
    'Test-FlashPlanConstraints',
    'Test-OutsideSlotUnchanged',
    'Test-TypedConfirmation',
    'Test-TypedRestoreConfirmation',
    'Parse-EfuseSecuritySummary',
    'Get-MonitorVerdict'
)