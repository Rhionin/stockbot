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
    with init 0xFFFFFFFF over the 4-byte ota_seq field only.
#>
Set-StrictMode -Version 2

$Script:PartitionEntryMagic = [byte[]](0xAA, 0x50)
$Script:Md5PartitionMagic = [byte[]](0xEB, 0xEB)
$Script:OtadataSectorSize = 0x1000
$Script:OtadataEntrySize = 32

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
    $crc = [uint64]([uint32]$Initial)
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
    return [uint32]$crc
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
    for ($i = 0; $i -lt $len; $i++) {
        if ($i -ge $SlotOffset -and $i -lt $slotPrefixEnd) {
            continue
        }
        if ($i -ge $SlotOffset -and $i -lt $slotEnd) {
            continue
        }
        if ($BeforeBytes[$i] -ne $AfterBytes[$i]) {
            throw ('Outside-slot byte changed at offset 0x{0:X}' -f $i)
        }
    }
    for ($i = $SlotOffset; $i -lt $slotPrefixEnd; $i++) {
        if ($BeforeBytes[$i] -ne $AfterBytes[$i]) {
            throw ('Inside-slot image prefix mismatch at 0x{0:X}' -f $i)
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
    $expected = ('RESTORE {0} {1}' -f $ExpectedOffsetHex.ToUpperInvariant(), $sha8)
    return ($normalized -ceq $expected)
}

function Parse-EfuseSecuritySummary {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)]
        [string]$SummaryText
    )
    if ([string]::IsNullOrWhiteSpace($SummaryText)) {
        throw 'STOP: empty eFuse summary'
    }
    $secureBoot = $null
    $flashCrypt = $null
    $lines = $SummaryText -split "`r?`n"
    foreach ($line in $lines) {
        if ($line -match 'SECURE_BOOT_EN\s*:\s*(\w+)') {
            $val = $Matches[1].ToLowerInvariant()
            $secureBoot = ($val -eq 'true' -or $val -eq '1')
        }
        if ($line -match 'SPI_BOOT_CRYPT_CNT\s*:\s*(\S+)') {
            $raw = $Matches[1]
            $bits = [uint32]0
            if ($raw -like '0x*') {
                $bits = [uint32][Convert]::ToUInt32($raw.Substring(2), 16)
            }
            else {
                $bits = [uint32][int]$raw
            }
            $flashCrypt = ((($bits -band [uint32]7) -ne 0))
        }
    }
    if ($null -eq $secureBoot -or $null -eq $flashCrypt) {
        throw 'STOP: unable to parse eFuse security fields'
    }
    $allowed = (-not $secureBoot) -and (-not $flashCrypt)
    if (-not $allowed) {
        throw 'STOP: secure boot or flash encryption enabled'
    }
    return [pscustomobject]@{
        SecureBootEnabled = $secureBoot
        FlashEncryption   = $flashCrypt
        Allowed           = $allowed
    }
}

Export-ModuleMember -Function @(
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
    'Parse-EfuseSecuritySummary'
)