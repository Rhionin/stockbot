package backup

import (
	"fmt"
	"os"
	"strings"
)

// DefaultBucket is the R2 bucket used when the settings file does not name one.
const DefaultBucket = "pantry-pi-backups"

// Config is read from the mounted settings file. R2 credentials are never
// taken from the environment.
type Config struct {
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	Endpoint        string
	HealthchecksURL string
	IncludeSecrets  bool
	SecretsPaths    []string
}

// LoadConfig reads the 0400 settings file. Group- and world-readable files
// are refused before the contents are read.
func LoadConfig(path string) (Config, error) {
	if err := ownerOnly(path); err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, errClass(ExitPreflight, "preflight", "could not read the backup settings file")
	}
	vals, err := parseSettings(data)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		AccountID:       vals["R2_ACCOUNT_ID"],
		AccessKeyID:     vals["R2_ACCESS_KEY_ID"],
		SecretAccessKey: vals["R2_SECRET_ACCESS_KEY"],
		Bucket:          vals["R2_BUCKET"],
		Endpoint:        vals["R2_ENDPOINT"],
		HealthchecksURL: vals["HC_BACKUP_RUN_URL"],
		IncludeSecrets:  truthy(vals["BACKUP_INCLUDE_SECRETS"]),
		SecretsPaths:    splitList(vals["BACKUP_SECRETS_PATHS"]),
	}
	if cfg.Bucket == "" {
		cfg.Bucket = DefaultBucket
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// ApplyOverrides replaces bucket, endpoint, and secrets settings when the
// caller passed flags. An empty flag leaves the file value in place.
// withSecrets is true when the flag was set; a false flag does not turn off
// a setting that the file enabled.
func (c Config) ApplyOverrides(bucket, endpoint string, withSecrets bool, paths []string) Config {
	if bucket != "" {
		c.Bucket = bucket
	}
	if endpoint != "" {
		c.Endpoint = endpoint
	}
	if withSecrets {
		c.IncludeSecrets = true
	}
	if len(paths) > 0 {
		c.SecretsPaths = append(append([]string{}, c.SecretsPaths...), paths...)
	}
	return c
}

// EndpointURL is the S3 API address. A settings endpoint wins. Otherwise the
// address is the account's R2 endpoint.
func (c Config) EndpointURL() (string, error) {
	if c.Endpoint != "" {
		return c.Endpoint, nil
	}
	if !validAccount(c.AccountID) {
		return "", errClass(ExitPreflight, "preflight", "the R2 account id is missing from the settings file")
	}
	return "https://" + c.AccountID + ".r2.cloudflarestorage.com", nil
}

func (c Config) validate() error {
	if c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return errClass(ExitPreflight, "preflight", "the R2 access key is missing from the settings file")
	}
	if strings.ContainsAny(c.AccessKeyID, " \t\r\n") || strings.ContainsAny(c.SecretAccessKey, "\r\n") {
		return errClass(ExitPreflight, "preflight", "the R2 access key is not a single line")
	}
	if !validBucket(c.Bucket) {
		return errClass(ExitPreflight, "preflight", "the backup bucket name is not valid")
	}
	if c.Endpoint == "" && !validAccount(c.AccountID) {
		return errClass(ExitPreflight, "preflight", "the R2 account id is missing from the settings file")
	}
	return nil
}

func validAccount(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

func validBucket(name string) bool {
	if len(name) < 3 || len(name) > 63 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
		default:
			return false
		}
	}
	return true
}

func parseSettings(data []byte) (map[string]string, error) {
	out := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return nil, errClass(ExitPreflight, "preflight", "a backup settings line is missing a value")
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if key == "" {
			return nil, errClass(ExitPreflight, "preflight", "a backup settings line is missing a name")
		}
		out[key] = val
	}
	return out, nil
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func ownerOnly(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return errClass(ExitPreflight, "preflight", "could not read the backup settings file")
	}
	if fi.IsDir() {
		return errClass(ExitPreflight, "preflight", "the backup settings path is a directory")
	}
	if insecureMode(fi.Mode()) {
		return errClass(ExitPreflight, "preflight", "the backup settings file must be readable only by its owner")
	}
	return nil
}

func insecureMode(mode os.FileMode) bool {
	return mode.Perm()&0o077 != 0
}

// LoadRecipients reads age public keys from a file and from an extra
// configuration string. Lines may be blank or start with #. A private key
// is rejected. Nothing is hard-coded.
func LoadRecipients(path, extra string) (string, error) {
	var b strings.Builder
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if extra == "" || !os.IsNotExist(err) {
				return "", errClass(ExitPreflight, "preflight", "could not read the age recipient file")
			}
		} else {
			b.Write(data)
			if len(data) > 0 && data[len(data)-1] != '\n' {
				b.WriteByte('\n')
			}
		}
	}
	if strings.TrimSpace(extra) != "" {
		b.WriteString(extra)
		b.WriteByte('\n')
	}
	text := filterRecipientText(b.String())
	if strings.TrimSpace(text) == "" {
		return "", errClass(ExitPreflight, "preflight", "no age recipient is configured")
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "AGE-SECRET-KEY-") {
			return "", errClass(ExitPreflight, "preflight", "the age recipient file contains a private key")
		}
		if !strings.HasPrefix(line, "age1") {
			return "", errClass(ExitPreflight, "preflight", "an age recipient is not a public key")
		}
	}
	return text, nil
}

func filterRecipientText(raw string) string {
	var b strings.Builder
	for _, line := range strings.Split(raw, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		fmt.Fprintf(&b, "%s\n", trim)
	}
	return b.String()
}
