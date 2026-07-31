package personal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/nextlevelbuilder/goclaw/internal/channels/zalo/personal/protocol"
	"github.com/nextlevelbuilder/goclaw/internal/config"
)

// authenticate resolves credentials and returns an authenticated session.
// Priority: preloaded (DB) > saved file > QR login.
func (c *Channel) authenticate(ctx context.Context) (*protocol.Session, error) {
	sess := protocol.NewSession()

	// 1. Preloaded credentials (from DB via factory).
	if c.preloadedCreds != nil {
		slog.Info("zalo_personal: attempting login with preloaded credentials")
		if err := protocol.LoginWithCredentials(ctx, sess, *c.preloadedCreds); err != nil {
			return nil, fmt.Errorf("preloaded credentials failed: %w", err)
		}
		c.persistRefreshedCredentials(sess)
		return sess, nil
	}

	// 2. Saved file credentials (config-based).
	credPath := c.resolveCredentialsPath()
	if cred := loadCredentials(credPath); cred != nil {
		slog.Info("zalo_personal: attempting login with saved credentials", "path", credPath)
		if err := protocol.LoginWithCredentials(ctx, sess, *cred); err != nil {
			slog.Warn("zalo_personal: saved credentials failed, falling back to QR", "error", err)
		} else {
			c.persistRefreshedCredentials(sess)
			return sess, nil
		}
	}

	// 3. QR login (interactive).
	slog.Info("zalo_personal: starting QR login. Scan the QR code with your Zalo app.")
	cred, err := protocol.LoginQR(ctx, sess, func(qrPNG []byte) {
		slog.Info("zalo_personal: QR code generated. Scan with Zalo app.", "size", len(qrPNG))
	})
	if err != nil {
		return nil, fmt.Errorf("QR login failed: %w", err)
	}

	// Save credentials for future re-login.
	if err := saveCredentials(credPath, cred); err != nil {
		slog.Warn("zalo_personal: failed to save credentials", "error", err, "path", credPath)
	} else {
		slog.Info("zalo_personal: credentials saved", "path", credPath)
	}

	return sess, nil
}

// SetPreloadedCredentials sets credentials from DB factory.
func (c *Channel) SetPreloadedCredentials(cred *protocol.Credentials) {
	c.preloadedCreds = cred
}

// SetCredentialPersister registers the callback used to write refreshed
// credentials back to wherever they came from (the channel_instances row for a
// DB-backed instance). Optional — a config-based channel persists to its file
// instead, and a nil persister simply skips the DB write.
func (c *Channel) SetCredentialPersister(fn func(any) error) {
	c.persistCreds = fn
}

// persistRefreshedCredentials snapshots the live cookie jar and writes it back.
//
// Zalo rotates session cookies during normal operation, but before this the
// rotated values existed only in memory: every restart replayed the ORIGINAL
// cookies captured at QR time, so the integration's lifetime was capped by that
// first cookie set and ended in a silent death (see listen.go's give-up path).
// Snapshotting after each successful login makes the session self-renewing.
//
// Best-effort throughout: this runs on the auth path, and failing to save a
// refreshed cookie must never prevent an otherwise-good login from proceeding.
// The worst case is simply the old behaviour.
func (c *Channel) persistRefreshedCredentials(sess *protocol.Session) {
	cred := protocol.ExportCredentials(sess)
	if cred == nil {
		// Refuses to export an empty/unusable jar rather than overwrite a good
		// saved set with a dud.
		return
	}

	// Keep the in-memory copy current so a later restart in this same process
	// replays the refreshed cookies even if persistence below fails.
	c.preloadedCreds = cred

	if c.persistCreds != nil {
		if err := c.persistCreds(cred); err != nil {
			slog.Warn("zalo_personal: could not persist refreshed credentials (session still valid in memory)",
				"channel", c.Name(), "error", err)
		}
		return
	}

	// Config-based channel: refresh the credentials file.
	if path := c.resolveCredentialsPath(); path != "" {
		if err := saveCredentials(path, cred); err != nil {
			slog.Warn("zalo_personal: could not save refreshed credentials", "path", path, "error", err)
		}
	}
}

func (c *Channel) resolveCredentialsPath() string {
	if c.config.CredentialsPath != "" {
		return config.ExpandHome(c.config.CredentialsPath)
	}
	return filepath.Join(config.ResolvedDataDirFromEnv(), "zalo-personal-credentials.json")
}

func loadCredentials(path string) *protocol.Credentials {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cred protocol.Credentials
	if err := json.Unmarshal(data, &cred); err != nil {
		slog.Warn("zalo_personal: invalid credentials file", "path", path, "error", err)
		return nil
	}
	if !cred.IsValid() {
		return nil
	}
	return &cred
}

func saveCredentials(path string, cred *protocol.Credentials) error {
	if cred == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cred, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
