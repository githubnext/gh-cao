package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"github.com/githubnext/gh-cao/server/internal/audit"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

const valuePublicationMaxBytes = 8 << 20

type operationalValuePublisher interface {
	PublishOperationalValue(context.Context, model.Row) (postgresx.ValuePublication, error)
}

type valuePublicationRequest struct {
	SchemaVersion int       `json:"schema_version"`
	Kind          string    `json:"kind"`
	Evidence      model.Row `json:"operational_value_evidence"`
}

func (a *App) publishValue(response http.ResponseWriter, request *http.Request) {
	if !a.adminAuthorized(request) {
		a.writeAdminDenied(response, request, "administrator access is required to publish value evidence")
		return
	}
	if a.valuePublisher == nil {
		writeError(request.Context(), response, http.StatusServiceUnavailable, "value publication requires PostgreSQL")
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" && mediaType != "application/x-ndjson" {
		writeError(request.Context(), response, http.StatusUnsupportedMediaType, "value publication requires application/json or application/x-ndjson")
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, valuePublicationMaxBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var input valuePublicationRequest
	err = decoder.Decode(&input)
	if err == nil {
		var trailing any
		err = decoder.Decode(&trailing)
		if errors.Is(err, io.EOF) {
			err = nil
		} else if err == nil {
			err = errors.New("multiple publication records")
		}
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(request.Context(), response, http.StatusRequestEntityTooLarge, "value evidence exceeds the publication limit")
		} else {
			writeError(request.Context(), response, http.StatusBadRequest, "publish exactly one original value evidence envelope")
		}
		return
	}
	if input.SchemaVersion != 2 || input.Kind != "operational_value_evidence" || len(input.Evidence) == 0 {
		writeError(request.Context(), response, http.StatusBadRequest, "publish exactly one original value evidence envelope")
		return
	}
	session, _ := request.Context().Value(oauthSessionContextKey{}).(oauthSession)
	if err := a.config.Audit.Record(request.Context(), "value.publish.requested", audit.Success,
		slog.String("actor", session.Login)); err != nil {
		if !errors.Is(err, audit.ErrMirrorUnavailable) {
			writeError(request.Context(), response, http.StatusServiceUnavailable, "publication audit is unavailable")
			return
		}
		serverLog.PrintfContext(request.Context(), "value publication audit mirror unavailable")
	}
	ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
	defer cancel()
	result, err := a.valuePublisher.PublishOperationalValue(ctx, input.Evidence)
	switch {
	case errors.Is(err, postgresx.ErrValueInvalid):
		writeError(request.Context(), response, http.StatusBadRequest, "value evidence is invalid")
	case errors.Is(err, postgresx.ErrValueConflict):
		writeError(request.Context(), response, http.StatusConflict, "value evidence conflicts with a retained capture, contract or immutable fact")
	case errors.Is(err, postgresx.ErrValueParentMissing):
		writeError(request.Context(), response, http.StatusNotFound, "value repository and workflow must already be observed")
	case err != nil:
		serverLog.PrintfContext(request.Context(), "value publication failed")
		writeError(request.Context(), response, http.StatusServiceUnavailable, "value publication is unavailable")
	default:
		if result.Updated {
			a.hub.Broadcast(result.Revision)
		}
		serverLog.PrintfContext(request.Context(), "value publication completed updated=%t revision=%d", result.Updated, result.Revision)
		writeJSON(response, http.StatusOK, result)
	}
}
