package backup

import (
	"context"
	"os"
)

// Fetch downloads one backup object and its manifest into local files.
// An empty objectKey selects the newest database object.
func Fetch(store ObjectStore, objectKey, outPath, manifestOut string) error {
	ctx := context.Background()
	if store == nil {
		return errClass(ExitPreflight, "preflight", "the backup destination is not configured")
	}
	if objectKey == "" {
		key, err := newestKey(ctx, store)
		if err != nil {
			return err
		}
		objectKey = key
	}
	body, err := store.Get(ctx, objectKey)
	if err != nil {
		return err
	}
	if err := writePrivate(outPath, body); err != nil {
		return err
	}
	if manifestOut == "" {
		return nil
	}
	mkey, err := manifestKeyFor(objectKey)
	if err != nil {
		return err
	}
	raw, err := store.Get(ctx, mkey)
	if err != nil {
		return err
	}
	return writePrivate(manifestOut, raw)
}

func newestKey(ctx context.Context, store ObjectStore) (string, error) {
	var all []ObjectInfo
	for _, prefix := range []string{"nightly/", "manual/"} {
		objs, err := store.List(ctx, prefix)
		if err != nil {
			return "", err
		}
		all = append(all, objs...)
	}
	best, found := selectNewestVerified(all)
	if !found {
		return "", errClass(ExitPreflight, "preflight", "there is no backup to download")
	}
	return best.Key, nil
}

func writePrivate(path string, body []byte) error {
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return errWrap(ExitUpload, "upload", "could not write the downloaded backup", err)
	}
	return nil
}
