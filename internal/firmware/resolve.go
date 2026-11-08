package firmware

import "strings"

const (
	ChannelStable = "stable"
	ChannelBeta   = "beta"
)

// Device is the firmware resolver input from a device's reported OTA fields.
type Device struct {
	Target              string
	Version             string
	Channel             string
	PinReleaseID        string
	RolledBackReleaseID string
	RetryReleaseID      string
}

// Release is one firmware release row offered to Resolve.
type Release struct {
	ID      string
	Target  string
	Version string
	Channel string
	Yanked  bool
}

// Resolve picks at most one release per firmware-ota.md §5 without I/O.
func Resolve(device Device, autoUpdate bool, releases []Release) *Release {
	if device.Target == "" {
		return nil
	}
	if device.PinReleaseID != "" {
		if r := findRelease(releases, device.PinReleaseID); r != nil {
			if !r.Yanked && r.Target == device.Target {
				return r
			}
		}
	}
	if !autoUpdate {
		return nil
	}
	best := selectHighestEligible(device, releases)
	if best == nil {
		return nil
	}
	if semverEqualPrecedence(best.Version, device.Version) {
		return nil
	}
	if device.RolledBackReleaseID != "" && best.ID == device.RolledBackReleaseID {
		if device.RetryReleaseID != device.RolledBackReleaseID {
			return nil
		}
	}
	return best
}

func findRelease(releases []Release, id string) *Release {
	for i := range releases {
		if releases[i].ID == id {
			return &releases[i]
		}
	}
	return nil
}

func selectHighestEligible(device Device, releases []Release) *Release {
	var best *Release
	for i := range releases {
		r := &releases[i]
		if r.ID == "" || r.Yanked || r.Target != device.Target {
			continue
		}
		if !channelEligible(device.Channel, r.Channel) {
			continue
		}
		if !validateSemVer2(r.Version) {
			continue
		}
		if best == nil {
			best = r
			continue
		}
		cmp := compareSemVerPrecedence(r.Version, best.Version)
		switch {
		case cmp > 0:
			best = r
		case cmp == 0 && strings.Compare(r.ID, best.ID) < 0:
			best = r
		}
	}
	return best
}

func channelEligible(deviceChannel, releaseChannel string) bool {
	switch deviceChannel {
	case ChannelStable:
		return releaseChannel == ChannelStable
	case ChannelBeta:
		return releaseChannel == ChannelStable || releaseChannel == ChannelBeta
	default:
		return false
	}
}
