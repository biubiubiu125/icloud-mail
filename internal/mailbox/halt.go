package mailbox

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
)

type createHaltRecord struct {
	AccountID   string    `json:"account_id"`
	Origin      string    `json:"origin"`
	Message     string    `json:"message"`
	AnonymousID string    `json:"anonymous_id"`
	Email       string    `json:"email"`
	At          time.Time `json:"at"`
}

func defaultHaltPath() string {
	if path := strings.TrimSpace(os.Getenv("ICLOUD_MAIL_HALT_FILE")); path != "" {
		return path
	}
	return filepath.Join(os.TempDir(), "icloud-mail-create-halt.json")
}

func (s *Service) recordCreateHalt(accountID, origin, message, anonymousID, email string) {
	accountID = strings.TrimSpace(accountID)
	if s == nil || accountID == "" {
		return
	}
	s.haltedAccounts.Store(accountID, struct{}{})
	if strings.TrimSpace(s.haltPath) == "" {
		return
	}
	s.haltFileMu.Lock()
	defer s.haltFileMu.Unlock()
	records, err := s.loadCreateHaltsUnlocked()
	if err != nil {
		s.logBackground("create", "halt_file", err)
		return
	}
	updated := false
	for i := range records {
		if records[i].AccountID != accountID {
			continue
		}
		updated = true
		if strings.TrimSpace(origin) != "" {
			records[i].Origin = origin
		}
		if strings.TrimSpace(message) != "" {
			records[i].Message = message
		}
		if strings.TrimSpace(anonymousID) != "" {
			records[i].AnonymousID = anonymousID
		}
		if strings.TrimSpace(email) != "" {
			records[i].Email = email
		}
		records[i].At = s.now()
	}
	if !updated {
		records = append(records, createHaltRecord{
			AccountID:   accountID,
			Origin:      strings.TrimSpace(origin),
			Message:     strings.TrimSpace(message),
			AnonymousID: strings.TrimSpace(anonymousID),
			Email:       strings.TrimSpace(email),
			At:          s.now(),
		})
	}
	if err := s.saveHaltFileUnlocked(records); err != nil {
		s.logBackground("create", "halt_file", err)
	}
}

func (s *Service) releaseCreateHalt(accountID string) {
	accountID = strings.TrimSpace(accountID)
	if s == nil || accountID == "" {
		return
	}
	s.haltedAccounts.Delete(accountID)
	if strings.TrimSpace(s.haltPath) == "" {
		return
	}
	s.haltFileMu.Lock()
	defer s.haltFileMu.Unlock()
	records, err := s.loadCreateHaltsUnlocked()
	if err != nil {
		s.logBackground("create", "halt_file", err)
		return
	}
	kept := make([]createHaltRecord, 0, len(records))
	for _, rec := range records {
		if rec.AccountID != accountID {
			kept = append(kept, rec)
		}
	}
	if len(kept) == 0 {
		if err := os.Remove(s.haltPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.logBackground("create", "halt_file", err)
		}
		return
	}
	if err := s.saveHaltFileUnlocked(kept); err != nil {
		s.logBackground("create", "halt_file", err)
	}
}

func (s *Service) restoreCreateHalts(ctx context.Context) error {
	if s == nil || strings.TrimSpace(s.haltPath) == "" {
		return nil
	}
	s.haltFileMu.Lock()
	defer s.haltFileMu.Unlock()
	records, err := s.loadCreateHaltsUnlocked()
	if err != nil {
		return err
	}
	kept := make([]createHaltRecord, 0, len(records))
	for _, rec := range records {
		accountID := strings.TrimSpace(rec.AccountID)
		if accountID == "" {
			continue
		}
		if s.DB != nil {
			err := s.DB.MarkReconciliation(ctx, accountID, rec.Origin, rec.Message, rec.AnonymousID, rec.Email, s.now())
			if err == nil {
				continue
			}
			s.logBackground("startup", "create_halt_mark", err)
		}
		s.haltedAccounts.Store(accountID, struct{}{})
		kept = append(kept, rec)
	}
	if len(kept) == len(records) {
		return nil
	}
	if len(kept) == 0 {
		if err := os.Remove(s.haltPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return s.saveHaltFileUnlocked(kept)
}

func (s *Service) loadCreateHaltsUnlocked() ([]createHaltRecord, error) {
	data, err := os.ReadFile(s.haltPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	var records []createHaltRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *Service) saveHaltFileUnlocked(records []createHaltRecord) error {
	if strings.TrimSpace(s.haltPath) == "" {
		return nil
	}
	data, err := json.Marshal(records)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.haltPath), 0o700); err != nil {
		return err
	}
	tmp := s.haltPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return replaceHaltFile(tmp, s.haltPath)
}

// haltFileBeforeReplace is a test seam. It runs immediately before the rename
// that replaces an existing halt file, so a crash cannot observe a missing file.
var haltFileBeforeReplace = func(string) {}

func replaceHaltFile(tmp, path string) error {
	haltFileBeforeReplace(path)
	if err := os.Rename(tmp, path); err == nil {
		return nil
	}
	// Windows cannot rename over an existing file. Remove only after that failure.
	if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		_ = os.Remove(tmp)
		return removeErr
	}
	return os.Rename(tmp, path)
}

func (s *Service) noteScheduledCreate(accountID string, err error) {
	if s == nil || err == nil {
		return
	}
	var app *apperr.Error
	if errors.As(err, &app) && app.Code == "mailbox_create_reconciliation_persist_failed" {
		s.haltCreate(accountID)
	}
	s.logBackground("schedule", "create_mailbox", err)
}
