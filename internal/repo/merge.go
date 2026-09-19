package repo

import (
	"github.com/sergi/go-diff/diffmatchpatch"
)

type blobStore interface {
	load(id string) (string, error)
	store(content string) (string, error)
}

func mergeIndex(base, head, theirs map[string]string, blobs blobStore) (map[string]string, error) {
	result := make(map[string]string, len(head))
	for name, id := range head {
		result[name] = id
	}

	set := func(name, id string) {
		if id == "" {
			delete(result, name)
		} else {
			result[name] = id
		}
	}

	done := make(map[string]bool)
	theirMoves := detectMoves(base, theirs)
	headMoves := detectMoves(base, head)

	for _, from := range sortedNames(theirMoves) {
		to := theirMoves[from]
		ours := head[to]

		if other, ok := headMoves[from]; ok && other != to {
			done[to] = true
			to, ours = other, head[other]
		} else if _, ok := head[to]; !ok {
			ours = head[from]
		}

		id, err := resolve(base[from], ours, theirs[theirMoves[from]], blobs)
		if err != nil {
			return nil, err
		}

		delete(result, from)
		set(to, id)
		done[from], done[to] = true, true
	}

	for _, from := range sortedNames(headMoves) {
		to := headMoves[from]
		if done[from] || done[to] || theirs[from] == "" {
			continue
		}

		id, err := resolve(base[from], head[to], theirs[from], blobs)
		if err != nil {
			return nil, err
		}

		delete(result, from)
		set(to, id)
		done[from], done[to] = true, true
	}

	for _, name := range sortedNames(union(base, head, theirs)) {
		if done[name] {
			continue
		}

		id, err := resolve(base[name], head[name], theirs[name], blobs)
		if err != nil {
			return nil, err
		}
		set(name, id)
	}
	return result, nil
}

func resolve(base, ours, theirs string, blobs blobStore) (string, error) {
	switch {
	case ours == theirs, theirs == base:
		return ours, nil
	case ours == base, ours == "":
		return theirs, nil
	case theirs == "":
		return ours, nil
	}

	baseContent, err := blobs.load(base)
	if err != nil {
		return "", err
	}
	ourContent, err := blobs.load(ours)
	if err != nil {
		return "", err
	}
	theirContent, err := blobs.load(theirs)
	if err != nil {
		return "", err
	}

	dmp := diffmatchpatch.New()
	merged, _ := dmp.PatchApply(dmp.PatchMake(baseContent, theirContent), ourContent)
	return blobs.store(merged)
}

func detectMoves(base, next map[string]string) map[string]string {
	moves := make(map[string]string)
	taken := make(map[string]bool)

	for _, from := range sortedNames(base) {
		if _, ok := next[from]; ok {
			continue
		}
		for _, to := range sortedNames(next) {
			if !taken[to] && base[from] == next[to] {
				if _, ok := base[to]; !ok {
					moves[from] = to
					taken[to] = true
					break
				}
			}
		}
	}
	return moves
}

func union(indexes ...map[string]string) map[string]string {
	all := make(map[string]string)
	for _, index := range indexes {
		for name, id := range index {
			all[name] = id
		}
	}
	return all
}
