package merger

import (
	"github.com/sergi/go-diff/diffmatchpatch"
)

// ThreeWayMerge merges the changes from base to client into server.
// It returns the merged text.
func ThreeWayMerge(base, client, server string) (string, error) {
	dmp := diffmatchpatch.New()

	// 1. Calculate patches: how did client change from base?
	patches := dmp.PatchMake(base, client)

	// 2. Apply patches to server version
	merged, _ := dmp.PatchApply(patches, server)

	// merged is the string, results is []bool indicating success/fail of patches
	// For this simple sync, we'll accept the best-effort merge.
	return merged, nil
}
