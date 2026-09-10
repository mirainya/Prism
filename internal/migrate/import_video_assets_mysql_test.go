//go:build integration

package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/tokenauth"
)

func verifyLegacyVideoAssetImport(t *testing.T, db *sql.DB) {
	t.Helper()
	now := time.Now().UTC()
	_, selector, secretDigest, err := tokenauth.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tokens (user_id,selector,secret_digest,secret_digest_version,auth_version,key_hint,name,balance,total_used,rate_limit,status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, 4242, selector, secretDigest, tokenauth.DigestVersion, 1, "test", "video import fixture", 0, 0, 60, 1, now, now); err != nil {
		t.Fatal(err)
	}
	var tokenID int64
	if err := db.QueryRow(`SELECT id FROM tokens WHERE selector=?`, selector).Scan(&tokenID); err != nil {
		t.Fatal(err)
	}
	sha := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if _, err := db.Exec(`INSERT INTO video_assets (id,token_id,sha256,size_bytes,kind,content_type,status,storage_path,expires_at,created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, "asset-integration-1", tokenID, sha, 12, "video", "video/mp4", "ready", "private/video/asset-integration-1.mp4", now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}
	options := ImportOptions{HMACKey: bytes.Repeat([]byte{73}, 32)}
	report, err := ImportLegacyVideoAssets(context.Background(), db, options)
	if err != nil || report.Imported != 1 || report.Issues != 0 {
		t.Fatalf("video asset import report=%+v err=%v", report, err)
	}
	var targetCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM gw_media_assets WHERE object_key=? AND user_id=? AND token_id=? AND content_length=? AND sha256=?`, "private/video/asset-integration-1.mp4", 4242, tokenID, 12, sha).Scan(&targetCount); err != nil {
		t.Fatal(err)
	}
	if targetCount != 1 {
		t.Fatalf("target media asset count=%d", targetCount)
	}
	var mapCount, revisionCount, proofCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM gw_migration_object_map WHERE source_table='video_assets' AND source_pk=? AND target_type='media_asset'`, "asset-integration-1").Scan(&mapCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM gw_migration_source_revisions r JOIN gw_migration_object_map m ON m.id=r.object_map_id WHERE m.source_table='video_assets' AND m.source_pk=?`, "asset-integration-1").Scan(&revisionCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM gw_migration_mapping_proofs p JOIN gw_migration_object_map m ON m.id=p.object_map_id WHERE m.source_table='video_assets' AND m.source_pk=?`, "asset-integration-1").Scan(&proofCount); err != nil {
		t.Fatal(err)
	}
	if mapCount != 1 || revisionCount != 1 || proofCount != 1 {
		t.Fatalf("mapping=%d revisions=%d proofs=%d", mapCount, revisionCount, proofCount)
	}
	audit, auditErr := DeepAudit(context.Background(), db)
	if auditErr != nil {
		t.Fatal(auditErr)
	}
	for _, table := range audit.UnverifiedLegacyHistory {
		if table == "video_assets" {
			t.Fatalf("video_assets should be verified after import: %+v", audit)
		}
	}
	second, secondErr := ImportLegacyVideoAssets(context.Background(), db, options)
	if secondErr != nil || second.RunID != report.RunID || second.Imported != 0 || second.Skipped != 1 || second.Issues != 0 {
		t.Fatalf("duplicate import report=%+v err=%v", second, secondErr)
	}
}
