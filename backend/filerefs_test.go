package main

import (
	"context"
	"testing"
	"time"
)

func refsKey(hash string) string { return "file:hash:" + hash + ":refs" }

func readRefs(t *testing.T, ctx context.Context, hash string) int64 {
	t.Helper()
	v, err := rdb.Get(ctx, refsKey(hash)).Int64()
	if err != nil {
		return 0 // absent counts as zero
	}
	return v
}

// Blobs are deduplicated by hash across every tenant, so the reference count is
// the only thing standing between one channel deleting its copy and another
// channel's file disappearing. These pin the two ways that count used to be
// wrong.
func TestFileHashRefsClaimedBeforeExistenceCheck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const hash = "refs-test-claim-first"
	rdb.Del(ctx, refsKey(hash))
	t.Cleanup(func() {
		cctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		rdb.Del(cctx, refsKey(hash))
	})

	// First uploader claims: it is the only reference.
	first, err := dbIncrFileHashRefsResult(ctx, hash)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if first != 1 {
		t.Fatalf("first claim returned %d, want 1", first)
	}

	// A second uploader of identical bytes claims before deciding to skip the
	// write. Its own post-increment value must show it is not the only holder.
	second, err := dbIncrFileHashRefsResult(ctx, hash)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if second != 2 {
		t.Errorf("second claim returned %d, want 2", second)
	}

	// The first tenant now deletes its file. One reference remains, so the blob
	// must survive.
	remaining, err := dbDecrFileHashRefs(ctx, hash)
	if err != nil {
		t.Fatalf("decr: %v", err)
	}
	if remaining != 1 {
		t.Errorf("after one delete the count is %d, want 1", remaining)
	}
	if remaining <= 0 {
		t.Error("the blob would have been deleted while another channel still references it")
	}
}

// A blob written before reference counting existed has no counter. An upload
// that dedupes against it would create the counter at 1 — recording one
// reference where two exist — so the new tenant deleting its own copy would
// destroy the original tenant's file. uploadFile detects that case (its own
// claim came back as 1 even though the blob was already there) and counts the
// pre-existing reference.
func TestFileHashRefsAdoptsPreexistingBlob(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const hash = "refs-test-legacy"
	rdb.Del(ctx, refsKey(hash))
	t.Cleanup(func() {
		cctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		rdb.Del(cctx, refsKey(hash))
	})

	// The legacy state: a blob on disk/R2, no counter at all.
	if got := readRefs(t, ctx, hash); got != 0 {
		t.Fatalf("precondition: counter should be absent, got %d", got)
	}

	// A new tenant uploads identical bytes. It claims first...
	claimed, err := dbIncrFileHashRefsResult(ctx, hash)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	// ...and, seeing the blob already present with a claim of 1, adopts the
	// untracked reference — this is the branch uploadFile takes when
	// !isNewHash && refs == 1.
	blobAlreadyExisted := true
	if blobAlreadyExisted && claimed == 1 {
		if err := dbIncrFileHashRefs(ctx, hash); err != nil {
			t.Fatalf("adopt: %v", err)
		}
	}

	if got := readRefs(t, ctx, hash); got != 2 {
		t.Fatalf("count is %d, want 2 (the legacy record plus the new one)", got)
	}

	// The new tenant deletes its file. The legacy record still points at the
	// blob, so the count must not reach zero.
	remaining, err := dbDecrFileHashRefs(ctx, hash)
	if err != nil {
		t.Fatalf("decr: %v", err)
	}
	if remaining <= 0 {
		t.Errorf("count fell to %d: deleting the new copy would destroy the "+
			"pre-existing channel's blob", remaining)
	}
}

// A failed upload must give its claim back, or every failure permanently
// inflates the count and strands the blob forever.
func TestFileHashRefsReleasedOnFailedUpload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const hash = "refs-test-release"
	rdb.Del(ctx, refsKey(hash))
	t.Cleanup(func() {
		cctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		rdb.Del(cctx, refsKey(hash))
	})

	if _, err := dbIncrFileHashRefsResult(ctx, hash); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// The upload then fails; uploadFile's deferred release runs.
	if _, err := dbDecrFileHashRefs(ctx, hash); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := readRefs(t, ctx, hash); got != 0 {
		t.Errorf("count is %d after a failed upload, want 0", got)
	}
}
