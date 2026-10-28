package mailbox

import (
	"context"
	"log/slog"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/domain"
)

func (s *Service) Run(ctx context.Context, logger *slog.Logger) {
	s.logger = logger
	if err := s.restoreCreateHalts(ctx); err != nil {
		s.logBackground("startup", "create_halt", err)
	}
	go s.loop(ctx, logger, "expire", 30*time.Second, s.expireOnce)
	go s.loop(ctx, logger, "watch", 0, s.watchOnce)
	go s.loop(ctx, logger, "schedule", 0, s.scheduleOnce)
}

func (s *Service) loop(ctx context.Context, logger *slog.Logger, name string, every time.Duration, fn func(context.Context) time.Duration) {
	for {
		wait := every
		if fn != nil {
			func() {
				defer func() {
					if recovered := recover(); recovered != nil && logger != nil {
						logger.Error("background task panicked", "task", name)
					}
				}()
				if next := fn(ctx); every == 0 && next > 0 {
					wait = next
				}
			}()
		}
		if wait <= 0 {
			wait = 30 * time.Second
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

type backgroundExpirer interface {
	ExpireHeldLeases(context.Context, time.Time) (int64, error)
	GetSettings(context.Context) (domain.Settings, error)
	DeleteOldMessages(context.Context, time.Time) (int64, error)
	NormalizeStalePendingDeletes(context.Context, time.Time) (int64, error)
}

func (s *Service) expireDB() backgroundExpirer {
	if s != nil && s.expirer != nil {
		return s.expirer
	}
	return s.DB
}

func (s *Service) expireOnce(ctx context.Context) time.Duration {
	now := s.now()
	db := s.expireDB()
	if _, err := db.ExpireHeldLeases(ctx, now); err != nil {
		s.logBackground("expire", "expire_leases", err)
	}
	if _, err := db.NormalizeStalePendingDeletes(ctx, now); err != nil {
		s.logBackground("expire", "stale_pending_delete", err)
	}
	settings, err := db.GetSettings(ctx)
	if err != nil {
		s.logBackground("expire", "settings", err)
		return 30 * time.Second
	}
	if settings.MessageRetentionDays > 0 {
		if _, err := db.DeleteOldMessages(ctx, now.AddDate(0, 0, -settings.MessageRetentionDays)); err != nil {
			s.logBackground("expire", "delete_old_messages", err)
		}
	}
	return 30 * time.Second
}

type backgroundCatalog interface {
	GetSettings(context.Context) (domain.Settings, error)
	ListMailboxes(context.Context, string, string) ([]domain.Mailbox, error)
	ListAccounts(context.Context) ([]domain.Account, error)
}

func (s *Service) catalogDB() backgroundCatalog {
	if s != nil && s.catalog != nil {
		return s.catalog
	}
	return s.DB
}

func (s *Service) watchOnce(ctx context.Context) time.Duration {
	settings, err := s.catalogDB().GetSettings(ctx)
	if err != nil {
		s.logBackground("watch", "settings", err)
		return 30 * time.Second
	}
	if !settings.WatcherEnabled {
		return 30 * time.Second
	}
	mailboxes, err := s.catalogDB().ListMailboxes(ctx, "", "")
	if err != nil {
		s.logBackground("watch", "list_mailboxes", err)
		return time.Duration(settings.WatcherIntervalSeconds) * time.Second
	}
	for _, mailbox := range mailboxes {
		if !mailbox.APIActive || mailbox.RemoteDeleteStatus == domain.DeletePending {
			continue
		}
		if _, err := s.syncMailbox(ctx, mailbox, settings.DefaultKeyword, settings.WebCodeSyncEnabled); err != nil {
			s.logBackground("watch", "sync_mailbox", err)
		}
	}
	return time.Duration(settings.WatcherIntervalSeconds)*time.Second + jitter(s.now(), 15*time.Second)
}

func (s *Service) scheduleOnce(ctx context.Context) time.Duration {
	settings, err := s.catalogDB().GetSettings(ctx)
	if err != nil {
		s.logBackground("schedule", "settings", err)
		return time.Minute
	}
	if !settings.SchedulerEnabled {
		return time.Minute
	}
	accounts, err := s.catalogDB().ListAccounts(ctx)
	if err != nil {
		s.logBackground("schedule", "list_accounts", err)
		return time.Duration(settings.SchedulerIntervalSeconds) * time.Second
	}
	for _, account := range accounts {
		if account.ReconciliationRequired || s.createHalted(account.ID) {
			continue
		}
		for i := 0; i < settings.SchedulerBatch; i++ {
			if _, err := s.CreateMailbox(ctx, account.ID, "", "scheduler", ""); err != nil {
				s.noteScheduledCreate(account.ID, err)
				break
			}
		}
	}
	return time.Duration(settings.SchedulerIntervalSeconds)*time.Second + jitter(s.now(), 20*time.Second)
}

func jitter(now time.Time, max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(now.UnixNano()%int64(max/time.Millisecond)) * time.Millisecond
}
