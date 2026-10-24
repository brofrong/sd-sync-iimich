package notify

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
)

// Center is a stub until this system's notifications land: it writes each
// notification to the log instead of showing it.
type Center struct{}

// NewCenter returns a Center that only logs; svc and onResponse are unused.
func NewCenter(svc *notifications.NotificationService, onResponse func(actionID string, data map[string]string)) *Center {
	return &Center{}
}

// Service is the Wails service to register with the application; it does
// nothing.
func (c *Center) Service() application.Service {
	return application.NewService(&stubService{})
}

// Send logs m.
func (c *Center) Send(m Message) {
	logx.Info("notify: notifications are not supported on this system yet", "id", m.ID, "title", m.Title, "body", m.Body)
}

// Remove does nothing: no notification was shown.
func (c *Center) Remove(id string) {}

// stubService is the service Center.Service registers.
type stubService struct{}

func (s *stubService) ServiceName() string { return "sd-sync-immich/notify" }
