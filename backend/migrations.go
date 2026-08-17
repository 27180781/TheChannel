package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"regexp"
	"time"

	"github.com/redis/go-redis/v9"
)

// migrationsAppliedKey holds the set of migration IDs that have already run.
const migrationsAppliedKey = "migrations:applied"

// channelFeaturesBackfillID is the one-shot backfill that derives the
// operator-controlled ChannelFeatures toggles from what each channel was
// actually configured to do before those toggles were enforced.
const channelFeaturesBackfillID = "2026-08-channel-features-backfill"

// fileChannelSlugBackfillID is the one-shot backfill that stamps the owning
// channel onto file records written before FileMetadata had a ChannelSlug.
const fileChannelSlugBackfillID = "2026-08-file-channel-slug-backfill"

// fileSlugFromMessagesID is the second file-ownership pass: it derives ownership
// from message content and channel logos, catching every legacy file that is in
// no channel:<slug>:files index (the index and ChannelSlug were introduced in
// the same commit, so index-only iteration visits essentially no legacy record).
const fileSlugFromMessagesID = "2026-08-file-slug-from-messages"

// channelFeaturesBackfillV2ID converges environments where the v1 backfill
// already ran with its old conditional logic (which enabled Webhook/
// Notifications/Ads only for channels using them at migration time, leaving
// late adopters silently dead). It unconditionally enables the same seven
// toggles the creation paths default to true.
const channelFeaturesBackfillV2ID = "2026-08-channel-features-backfill-v2"

// runMigrations applies any data migration that has not run yet.
//
// It is deliberately non-fatal: a migration that fails is simply left unmarked
// and retried on the next boot, rather than taking the server down. Callers run
// it before the HTTP listener starts, so a successful run is always complete
// before the first request is served.
func runMigrations(ctx context.Context) {
	if os.Getenv("SKIP_MIGRATIONS") == "1" {
		log.Println("migrations: skipped (SKIP_MIGRATIONS=1)")
		return
	}

	migrations := []struct {
		id  string
		run func(context.Context) error
	}{
		{channelFeaturesBackfillID, backfillChannelFeatures},
		{fileChannelSlugBackfillID, backfillFileChannelSlugs},
		{fileSlugFromMessagesID, backfillFileSlugsFromMessages},
		{channelFeaturesBackfillV2ID, backfillChannelFeaturesV2},
	}

	for _, m := range migrations {
		applied, err := rdb.SIsMember(ctx, migrationsAppliedKey, m.id).Result()
		if err != nil {
			log.Printf("migrations: cannot read applied set, skipping %s this boot: %v", m.id, err)
			continue
		}
		if applied {
			continue
		}

		log.Printf("migrations: running %s", m.id)
		if err := m.run(ctx); err != nil {
			log.Printf("migrations: %s FAILED (will retry on next start): %v", m.id, err)
			continue
		}
		if err := rdb.SAdd(ctx, migrationsAppliedKey, m.id).Err(); err != nil {
			log.Printf("migrations: %s succeeded but could not be marked applied, it will run again: %v", m.id, err)
			continue
		}
		log.Printf("migrations: %s completed", m.id)
	}
}

// settingBool reports whether key is present and truthy, using the same
// parsing the rest of the app uses (booleans are persisted as "1").
func settingBool(settings Settings, key string) bool {
	for i := range settings {
		if settings[i].Key == key {
			return settings[i].GetBool()
		}
	}
	return false
}

// backfillChannelFeatures turns on the ChannelFeatures toggles that were never
// enforced before the flags shipped.
//
// Why this is needed: createChannel and approveChannelRequest only ever set
// Reactions, FileUploads, Reports and ScheduledMessages, so Ads, Notifications
// and Webhook are false on every channel that exists today. Those three were
// never enforced, so channels have been happily using them with the flag off.
// Enforcing them without this backfill would switch the features off platform
// wide.
//
// All seven toggles are enabled unconditionally, matching the creation-path
// defaults: the flags are pure super-admin kill switches, and a channel that
// first configures a webhook/push/ad AFTER the deploy must not find the feature
// silently dead because it happened to be unconfigured at migration time.
//
// The migration only ever turns a flag ON. It runs exactly once, so a super
// admin who later turns something off is never overridden.
func backfillChannelFeatures(ctx context.Context) error {
	channels, err := dbListChannels(ctx)
	if err != nil {
		return fmt.Errorf("list channels: %w", err)
	}

	// require_auth / require_auth_for_view_files / count_views were removed from
	// the owner settings form, so any surviving value is leftover configuration
	// the owner can neither see nor undo. Honouring it would wall a channel off
	// or start 401ing file requests that serve today, with no way back through
	// the UI — unlike the ads/notifications/webhook part above, which only keeps
	// live behaviour alive. So it is opt-in, for an operator who knows the
	// leftovers are still what their owners want.
	applyAuthFlags := os.Getenv("MIGRATION_APPLY_AUTH_FLAGS") == "1"
	if applyAuthFlags {
		log.Println("migrations: MIGRATION_APPLY_AUTH_FLAGS=1, applying leftover auth/view settings to features")
	}

	var changed int
	for _, ch := range channels {
		settings, err := dbGetSettings(ctx, ch.Slug)
		if err != nil {
			// Do not mark the migration applied if we could not read a channel.
			return fmt.Errorf("get settings for %s: %w", ch.Slug, err)
		}

		before := ch.Features
		f := ch.Features

		// Reactions, uploads, reports and scheduling were available to everyone
		// before requireFeature started gating them, and nothing outside
		// dbCreateChannel ever set them — so a channel whose features blob was
		// written before that would silently lose all four on the first boot
		// after the gates ship. Webhook, Notifications and Ads were likewise
		// unenforced pre-deploy, so enabling them preserves live behaviour for
		// every channel and keeps them pure super-admin kill switches.
		f.Reactions = true
		f.FileUploads = true
		f.Reports = true
		f.ScheduledMessages = true
		f.Webhook = true
		f.Notifications = true
		f.Ads = true

		if applyAuthFlags {
			// These three keys were offered in the channel settings UI but the
			// backend only ever read the Features equivalents, so the owner's
			// choice has silently had no effect until now.
			if settingBool(settings, "count_views") {
				f.CountViews = true
			}
			if settingBool(settings, "require_auth") {
				f.RequireAuth = true
			}
			if settingBool(settings, "require_auth_for_view_files") {
				f.RequireAuthFiles = true
			}
		}

		if f == before {
			continue
		}

		if err := dbSetChannelFeatures(ctx, ch.Slug, &f); err != nil {
			return fmt.Errorf("set features for %s: %w", ch.Slug, err)
		}
		changed++
		log.Printf("migrations: %s features updated: %+v -> %+v", ch.Slug, before, f)
	}

	log.Printf("migrations: channel features backfill touched %d of %d channels", changed, len(channels))
	return nil
}

// backfillChannelFeaturesV2 unconditionally enables the seven default-on
// toggles. It exists because v1 originally enabled Webhook/Notifications/Ads
// only for channels using them at migration time; environments where that
// version already ran (and marked itself applied) still converge through this
// second pass. Like v1 it only ever turns flags ON and runs exactly once, so a
// super admin who turns something off afterwards is never overridden.
func backfillChannelFeaturesV2(ctx context.Context) error {
	channels, err := dbListChannels(ctx)
	if err != nil {
		return fmt.Errorf("list channels: %w", err)
	}

	var changed int
	for _, ch := range channels {
		before := ch.Features
		f := ch.Features
		f.Reactions = true
		f.FileUploads = true
		f.Reports = true
		f.ScheduledMessages = true
		f.Webhook = true
		f.Notifications = true
		f.Ads = true

		if f == before {
			continue
		}
		if err := dbSetChannelFeatures(ctx, ch.Slug, &f); err != nil {
			return fmt.Errorf("set features for %s: %w", ch.Slug, err)
		}
		changed++
		log.Printf("migrations: %s features updated: %+v -> %+v", ch.Slug, before, f)
	}

	log.Printf("migrations: channel features backfill v2 touched %d of %d channels", changed, len(channels))
	return nil
}

// backfillFileChannelSlugs stamps the owning channel onto file records that
// predate FileMetadata.ChannelSlug.
//
// Why this is needed: fileVisibleToChannel now denies any record whose
// ChannelSlug does not match the channel serving it, and every file uploaded
// before that field existed has an empty one. Without this backfill, historical
// attachments and channel logos 404 on the first boot after deploy, and the only
// way out is ALLOW_LEGACY_UNSCOPED_FILES, which re-opens the cross-tenant read
// the isolation check was written to close.
//
// Ownership is taken from the per-channel file index each record is already
// listed in, so nothing is guessed. Records that already carry a slug are left
// alone. As with the features backfill, a read/write failure returns the error
// and leaves the ID unmarked so the whole thing is retried on the next boot.
func backfillFileChannelSlugs(ctx context.Context) error {
	channels, err := dbListChannels(ctx)
	if err != nil {
		return fmt.Errorf("list channels: %w", err)
	}

	var stamped int
	for _, ch := range channels {
		members, err := rdb.ZRange(ctx, channelFilesKey(ch.Slug), 0, -1).Result()
		if err != nil {
			return fmt.Errorf("list files for %s: %w", ch.Slug, err)
		}

		for _, m := range members {
			fileID, _, ok := decodeFileMember(m)
			if !ok {
				continue
			}
			// dbGetFileMetadata's YAML fallback slices id[:4]; a malformed short
			// member must be skipped, not crash-loop the boot (see db.go's
			// identical guard in dbDeleteChannel).
			if len(fileID) < 4 {
				log.Printf("migrations: %s: malformed file index member %q, skipping", ch.Slug, m)
				continue
			}

			meta, err := dbGetFileMetadata(ctx, fileID)
			if err != nil {
				// A tracked id whose metadata is gone (neither in Redis nor on
				// disk) is index debris, not a reason to abandon the run.
				log.Printf("migrations: %s: no metadata for tracked file %s, skipping", ch.Slug, fileID)
				continue
			}
			if meta.ChannelSlug != "" {
				continue
			}

			// A YAML-era record reaches this point through the fallback path,
			// which does not populate ChannelSlug either; saving it promotes it
			// into Redis with the owner attached.
			meta.ChannelSlug = ch.Slug
			if err := dbSaveFileMetadata(ctx, meta); err != nil {
				return fmt.Errorf("save metadata for %s: %w", fileID, err)
			}
			stamped++
		}
	}

	log.Printf("migrations: stamped channel ownership onto %d legacy file records across %d channels", stamped, len(channels))
	return nil
}

// fileRefRegexes extract file IDs from message text: the channel-scoped URL
// uploads have produced since files became per-channel, and the legacy global
// one older messages still embed.
var (
	channelFileRefRegex = regexp.MustCompile(`/api/channel/[a-z0-9\-]+/files/([0-9a-fA-F]{4,})`)
	legacyFileRefRegex  = regexp.MustCompile(`/api/files/([0-9a-fA-F]{4,})`)
)

// backfillFileSlugsFromMessages is the second ownership pass. The index-based
// backfill above visits only channel:<slug>:files members, but the index and
// FileMetadata.ChannelSlug were introduced by the same commit — so every
// genuinely legacy record (YAML-era files and pre-index Redis records) is in no
// index at all and would 404 forever. This pass derives ownership from what the
// channels actually reference: file URLs embedded in message text and each
// channel's logo. Records that already carry a slug are left alone; IDs
// referenced by no channel stay denied.
func backfillFileSlugsFromMessages(ctx context.Context) error {
	channels, err := dbListChannels(ctx)
	if err != nil {
		return fmt.Errorf("list channels: %w", err)
	}

	var stamped int
	stampFile := func(slug, fileID string) error {
		if len(fileID) < 4 {
			return nil
		}
		meta, err := dbGetFileMetadata(ctx, fileID)
		if err != nil {
			// A referenced id with no surviving metadata anywhere is debris,
			// not a reason to abandon the run.
			return nil
		}
		if meta.ChannelSlug != "" {
			return nil
		}
		// A YAML-era record reaches this point through the fallback path;
		// saving it promotes it into Redis with the owner attached.
		meta.ChannelSlug = slug
		if err := dbSaveFileMetadata(ctx, meta); err != nil {
			return fmt.Errorf("save metadata for %s: %w", fileID, err)
		}
		stamped++
		return nil
	}

	for _, ch := range channels {
		// The channel's logo is a file reference too.
		if ch.LogoUrl != "" {
			for _, re := range []*regexp.Regexp{channelFileRefRegex, legacyFileRefRegex} {
				for _, m := range re.FindAllStringSubmatch(ch.LogoUrl, -1) {
					if err := stampFile(ch.Slug, m[1]); err != nil {
						return err
					}
				}
			}
		}

		// m_times members are the full message hash keys.
		messageKeys, err := rdb.ZRange(ctx, fmt.Sprintf("channel:%s:m_times", ch.Slug), 0, -1).Result()
		if err != nil {
			return fmt.Errorf("list messages for %s: %w", ch.Slug, err)
		}
		for _, mk := range messageKeys {
			text, err := rdb.HGet(ctx, mk, "text").Result()
			if err != nil {
				if err == redis.Nil {
					continue
				}
				return fmt.Errorf("read message %s: %w", mk, err)
			}
			for _, re := range []*regexp.Regexp{channelFileRefRegex, legacyFileRefRegex} {
				for _, m := range re.FindAllStringSubmatch(text, -1) {
					if err := stampFile(ch.Slug, m[1]); err != nil {
						return err
					}
				}
			}
		}
	}

	log.Printf("migrations: stamped channel ownership onto %d legacy file records referenced by messages/logos", stamped)
	return nil
}

// migrationContext is the budget for the whole migration step at boot.
func migrationContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 2*time.Minute)
}
