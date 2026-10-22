package app

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/immich"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/uploader"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

// Code identifies why sync is (or isn't) running, independent of language.
// The GUI looks it up in a translation catalog together with Params.
type Code string

const (
	CodeNotConfigured     Code = "not_configured"
	CodeNoCardSelected    Code = "no_card_selected"
	CodeWaitingForCard    Code = "waiting_for_card"
	CodeOtherCardMounted  Code = "other_card_mounted"
	CodeScanning          Code = "scanning"
	CodeSyncing           Code = "syncing"
	CodeNothingNew        Code = "nothing_new"
	CodeDone              Code = "done"
	CodeDoneWithErrors    Code = "done_with_errors"
	CodeServerUnreachable Code = "server_unreachable"
	CodeAuthFailed        Code = "auth_failed"
	CodeServerError       Code = "server_error"
	CodeCardRemoved       Code = "card_removed"
	CodeCancelled         Code = "cancelled"
	CodeFailed            Code = "failed"
)

// Action is a follow-up the GUI can offer for a given Code (see actionsFor).
type Action string

const (
	ActionRetry      Action = "retry"
	ActionStop       Action = "stop"
	ActionSelectCard Action = "select_card"
	ActionOpenServer Action = "open_server"
	ActionOpenCards  Action = "open_cards"
	ActionEject      Action = "eject"
	ActionOpenImmich Action = "open_immich"
	ActionOpenLogs   Action = "open_logs"
)

// Progress describes an in-flight sync. It is nil on Status unless Code is
// CodeSyncing.
type Progress struct {
	Current     int     `json:"current"`
	Total       int     `json:"total"`
	BytesDone   int64   `json:"bytesDone"`
	BytesTotal  int64   `json:"bytesTotal"`
	BytesPerSec float64 `json:"bytesPerSec"`
	ETASeconds  int     `json:"etaSeconds"`
	File        string  `json:"file"`
}

// Status is the structured, language-agnostic state of the app. The GUI
// renders Code (plus Params for substitution) through its own translation
// catalog instead of receiving pre-formatted text from Go.
type Status struct {
	Code     Code              `json:"code"`
	Params   map[string]string `json:"params"`
	Progress *Progress         `json:"progress"`
	Actions  []Action          `json:"actions"`
	At       time.Time         `json:"at"`
}

// actionsFor returns the follow-up actions the GUI should offer for code.
// It is shared by IdleStatus and ResultStatus (and reused for the startup
// status in a later task) so the action set per code is defined once.
func actionsFor(code Code) []Action {
	switch code {
	case CodeNotConfigured:
		return []Action{ActionOpenServer}
	case CodeNoCardSelected:
		return []Action{ActionOpenCards}
	case CodeOtherCardMounted:
		return []Action{ActionSelectCard}
	case CodeDone:
		return []Action{ActionEject, ActionOpenImmich}
	case CodeDoneWithErrors:
		return []Action{ActionOpenLogs}
	case CodeServerUnreachable, CodeServerError:
		return []Action{ActionRetry}
	case CodeAuthFailed:
		return []Action{ActionOpenServer}
	case CodeFailed:
		return []Action{ActionOpenLogs}
	case CodeSyncing:
		return []Action{ActionStop}
	default:
		return []Action{}
	}
}

// newStatus builds a Status for code, filling in Actions and At. params may
// be nil; it is normalized to an empty (non-nil) map so the field always
// serializes as a JSON object.
func newStatus(code Code, params map[string]string) Status {
	if params == nil {
		params = map[string]string{}
	}
	return Status{
		Code:    code,
		Params:  params,
		Actions: actionsFor(code),
		At:      time.Now(),
	}
}

// IdleStatus reports why sync is not currently running, based on
// configuration and the list of currently mounted removable volumes.
//
// ok is false when the selected card is mounted: the caller should keep
// showing whatever the last sync attempt produced (or start a new sync)
// rather than an idle status.
func IdleStatus(cfg config.Config, mounted []volume.Info) (Status, bool) {
	if !cfg.ServerConfigured() {
		return newStatus(CodeNotConfigured, nil), true
	}
	if cfg.Watch.VolumeUUID == "" {
		return newStatus(CodeNoCardSelected, nil), true
	}

	for _, v := range mounted {
		if strings.EqualFold(v.UUID, cfg.Watch.VolumeUUID) {
			return Status{}, false
		}
	}

	if len(mounted) > 0 {
		other := mounted[0]
		return newStatus(CodeOtherCardMounted, map[string]string{
			"selected_name": cfg.Watch.VolumeName,
			"mounted_name":  other.Name,
			"mounted_uuid":  other.UUID,
		}), true
	}

	return newStatus(CodeWaitingForCard, map[string]string{
		"name": cfg.Watch.VolumeName,
	}), true
}

// ResultStatus turns the outcome of a sync attempt into a Status. serverHost
// is the host part of the configured Immich URL; callers compute it since
// ResultStatus does not parse config.
func ResultStatus(res uploader.Result, err error, cardRemoved bool, serverHost string) Status {
	if err == nil {
		switch {
		case res.Uploaded == 0 && res.Errors == 0:
			return newStatus(CodeNothingNew, nil)
		case res.Errors > 0:
			return newStatus(CodeDoneWithErrors, map[string]string{
				"uploaded": strconv.Itoa(res.Uploaded),
				"errors":   strconv.Itoa(res.Errors),
			})
		default:
			return newStatus(CodeDone, map[string]string{
				"uploaded": strconv.Itoa(res.Uploaded),
			})
		}
	}

	if errors.Is(err, context.Canceled) {
		params := map[string]string{"uploaded": strconv.Itoa(res.Uploaded)}
		if cardRemoved {
			return newStatus(CodeCardRemoved, params)
		}
		return newStatus(CodeCancelled, params)
	}

	if errors.Is(err, uploader.ErrAuthFailed) {
		return newStatus(CodeAuthFailed, nil)
	}

	if errors.Is(err, uploader.ErrServerUnavailable) {
		var se *immich.StatusError
		if errors.As(err, &se) {
			return newStatus(CodeServerError, map[string]string{
				"status": strconv.Itoa(se.Code),
				"detail": immich.Detail(err),
			})
		}
		return newStatus(CodeServerUnreachable, map[string]string{
			"host":   serverHost,
			"detail": immich.Detail(err),
		})
	}

	return newStatus(CodeFailed, map[string]string{
		"detail": immich.Detail(err),
	})
}
