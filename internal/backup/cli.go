package backup

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/buildinfo"
)

// cmdRunRunnerHook is set by tests to observe the Runner cmdRun builds before Run.
var cmdRunRunnerHook func(*Runner)

// CLI runs stockbot-backup. args are the arguments after the program name.
func CLI(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: stockbot-backup run|verify|stale|fetch|cleanup-stage|version")
		return ExitUsage
	}
	switch args[0] {
	case "run":
		return cmdRun(args[1:])
	case "verify":
		return cmdVerify(args[1:])
	case "stale":
		return cmdStale(args[1:])
	case "fetch":
		return cmdFetch(args[1:])
	case "cleanup-stage":
		return cmdCleanup(args[1:])
	case "version", "-version", "--version":
		fmt.Printf("stockbot-backup %s %s\n", buildinfo.VisibleVersion(), buildinfo.Commit)
		return ExitOK
	case "-h", "--help", "help":
		fmt.Fprintln(os.Stderr, "usage: stockbot-backup run|verify|stale|fetch|cleanup-stage|version")
		return ExitOK
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", args[0])
		return ExitUsage
	}
}

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	db := fs.String("db", "", "pantry database path")
	stage := fs.String("stage", DefaultStageDir, "tmpfs staging directory")
	reason := fs.String("reason", "nightly", "nightly, manual, pre-deploy, or drill")
	secrets := fs.String("secrets", "/run/secrets/backup/r2.env", "settings file")
	recipients := fs.String("recipients", "/etc/backup/recipients", "age public keys")
	digest := fs.String("image-digest", "", "image id recorded in the manifest")
	limit := fs.Int64("tmpfs-limit", 0, "tmpfs size in bytes; 0 detects the filesystem")
	withSecrets := fs.Bool("with-secrets", false, "also archive the configured secret paths")
	var paths stringList
	fs.Var(&paths, "secrets-path", "path to include when secrets archiving is enabled")
	prune := fs.Bool("prune-premigration", false, "delete pantry-pre-*.db copies; off by default")
	busy := fs.Duration("busy-timeout", 10*time.Second, "SQLite busy timeout")
	retries := fs.Int("busy-retries", 3, "extra attempts when the database is busy")
	retryWait := fs.Duration("busy-retry-wait", 30*time.Second, "wait between busy attempts")
	bucket := fs.String("bucket", "", "bucket name; overrides the settings file")
	endpoint := fs.String("endpoint", "", "S3 endpoint; overrides the settings file")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "could not read the backup command")
		return ExitUsage
	}
	cfg, recipientText, store, err := openStore(*secrets, *recipients, os.Getenv("BACKUP_AGE_RECIPIENT"), *bucket, *endpoint)
	if err != nil {
		return fail(err)
	}
	cfg = cfg.ApplyOverrides(*bucket, *endpoint, *withSecrets, paths)
	if err := cfg.validate(); err != nil {
		return fail(err)
	}
	if *retries < 0 {
		fmt.Fprintln(os.Stderr, "busy-retries cannot be negative")
		return ExitUsage
	}
	busyRetries := *retries
	runner := &Runner{
		DBPath:            *db,
		StageDir:          *stage,
		TmpfsLimit:        *limit,
		Reason:            *reason,
		Recipients:        recipientText,
		Store:             store,
		BusyTimeout:       *busy,
		BusyRetries:       &busyRetries,
		BusyRetryWait:     *retryWait,
		ImageDigest:       *digest,
		PrunePremigration: *prune,
		IncludeSecrets:    cfg.IncludeSecrets,
		SecretsPaths:      cfg.SecretsPaths,
		HealthURL:         cfg.HealthchecksURL,
	}
	if cmdRunRunnerHook != nil {
		cmdRunRunnerHook(runner)
	}
	if _, err := runner.Run(context.Background()); err != nil {
		return fail(err)
	}
	return ExitOK
}

func cmdVerify(args []string) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	db := fs.String("db", "", "restored database")
	manifest := fs.String("manifest", "", "manifest json")
	if err := fs.Parse(args); err != nil || *db == "" || *manifest == "" {
		fmt.Fprintln(os.Stderr, "verify needs a database and a manifest")
		return ExitUsage
	}
	if err := Verify(*db, *manifest); err != nil {
		return fail(err)
	}
	return ExitOK
}

func cmdStale(args []string) int {
	fs := flag.NewFlagSet("stale", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	secrets := fs.String("secrets", "/run/secrets/backup/r2.env", "settings file")
	bucket := fs.String("bucket", "", "bucket name")
	endpoint := fs.String("endpoint", "", "S3 endpoint")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "could not read the stale-backup command")
		return ExitUsage
	}
	cfg, _, store, err := openStore(*secrets, "", "", *bucket, *endpoint)
	if err != nil {
		return fail(err)
	}
	cfg = cfg.ApplyOverrides(*bucket, *endpoint, false, nil)
	runner := &Runner{Store: store, HealthURL: cfg.HealthchecksURL}
	if err := runner.CheckStale(context.Background()); err != nil {
		runner.ping(context.Background(), false, "class="+AlertClass(err))
		return fail(err)
	}
	fmt.Println("backup fresh")
	return ExitOK
}

func cmdFetch(args []string) int {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	secrets := fs.String("secrets", "/run/secrets/backup/r2.env", "settings file")
	object := fs.String("object", "", "object key; empty downloads the newest")
	out := fs.String("out", "", "ciphertext destination")
	manifestOut := fs.String("manifest-out", "", "manifest destination")
	bucket := fs.String("bucket", "", "bucket name")
	endpoint := fs.String("endpoint", "", "S3 endpoint")
	if err := fs.Parse(args); err != nil || *out == "" {
		fmt.Fprintln(os.Stderr, "fetch needs an output path")
		return ExitUsage
	}
	_, _, store, err := openStore(*secrets, "", "", *bucket, *endpoint)
	if err != nil {
		return fail(err)
	}
	if err := Fetch(store, *object, *out, *manifestOut); err != nil {
		return fail(err)
	}
	fmt.Println("fetch ok")
	return ExitOK
}

func cmdCleanup(args []string) int {
	fs := flag.NewFlagSet("cleanup-stage", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	db := fs.String("db", "", "pantry database path")
	if err := fs.Parse(args); err != nil || *db == "" {
		fmt.Fprintln(os.Stderr, "cleanup-stage needs the database path")
		return ExitUsage
	}
	if err := CleanupStage(*db); err != nil {
		return fail(err)
	}
	return ExitOK
}

func openStore(secretsPath, recipientsPath, recipientEnv, bucket, endpoint string) (Config, string, ObjectStore, error) {
	cfg, err := LoadConfig(secretsPath)
	if err != nil {
		return Config{}, "", nil, err
	}
	cfg = cfg.ApplyOverrides(bucket, endpoint, false, nil)
	if err := cfg.validate(); err != nil {
		return Config{}, "", nil, err
	}
	var recipientText string
	if recipientsPath != "" || strings.TrimSpace(recipientEnv) != "" {
		recipientText, err = LoadRecipients(recipientsPath, recipientEnv)
		if err != nil {
			return Config{}, "", nil, err
		}
	}
	api, err := cfg.EndpointURL()
	if err != nil {
		return Config{}, "", nil, err
	}
	store, err := NewS3Store(api, cfg.Bucket, cfg.AccessKeyID, cfg.SecretAccessKey, http.DefaultClient)
	if err != nil {
		return Config{}, "", nil, err
	}
	return cfg, recipientText, store, nil
}

func fail(err error) int {
	fmt.Fprintf(os.Stderr, "backup failed class=%s\n", AlertClass(err))
	return ExitCode(err)
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}
