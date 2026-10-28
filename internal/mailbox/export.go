package mailbox

import (
	"context"
	"time"

	"github.com/biubiubiu125/icloud-mail/internal/apperr"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/store"
)

// RestoreContext stays usable after the caller disconnects, so an export
// restore is not canceled by the request that failed to write.
func RestoreContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(parent), 15*time.Second)
}

func (s *Service) Export(ctx context.Context, ids []string, baseURL string) ([]domain.ExportItem, map[string]store.ExportStamp, error) {
	if len(ids) == 0 {
		return nil, nil, apperr.New(400, "export_selection_missing", "请明确选择要导出的邮箱", false)
	}
	if len(ids) > 500 {
		return nil, nil, apperr.New(400, "export_selection_too_large", "一次最多导出 500 个邮箱", false)
	}
	seen := map[string]struct{}{}
	clean := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			return nil, nil, apperr.New(400, "export_selection_missing", "导出选择包含空邮箱", false)
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		clean = append(clean, id)
	}
	for _, id := range clean {
		if _, ok, err := s.DB.GetMailbox(ctx, id); err != nil {
			return nil, nil, err
		} else if !ok {
			return nil, nil, apperr.New(400, "export_selection_missing", "导出选择里有不存在的邮箱", false)
		}
	}
	previous, err := s.DB.MarkExported(ctx, clean, s.now())
	if err != nil {
		return nil, nil, err
	}
	items := make([]domain.ExportItem, 0, len(clean))
	for _, id := range clean {
		item, ok, err := s.DB.GetMailbox(ctx, id)
		if err != nil || !ok {
			restoreCtx, cancel := RestoreContext(ctx)
			restoreErr := s.DB.RestoreExported(restoreCtx, previous)
			cancel()
			if restoreErr != nil {
				return nil, nil, restoreErr
			}
			if err == nil {
				err = apperr.New(400, "export_selection_missing", "导出选择里有不存在的邮箱", false)
			}
			return nil, nil, err
		}
		items = append(items, domain.ExportItem{
			Email:    item.Email,
			APIURL:   s.APIURL(baseURL, item.Email, item.APIToken),
			APIToken: item.APIToken,
		})
	}
	return items, previous, nil
}
