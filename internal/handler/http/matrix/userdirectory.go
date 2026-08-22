package matrix

import (
	"errors"
	"net/http"

	validation "github.com/go-ozzo/ozzo-validation/v4"

	"github.com/thelemail/thaumaste/internal/entity"
)

type directoryRequest struct {
	SearchTerm string `json:"search_term"`
	Limit      int    `json:"limit"`
}

type directoryResult struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`
}

type directoryResponse struct {
	Results []directoryResult `json:"results"`
	Limited bool              `json:"limited"`
}

func (h *Handler) searchUsers(w http.ResponseWriter, r *http.Request) {
	tenant, caller, ok := h.callerAndTenant(w, r)
	if !ok {
		return
	}

	var body directoryRequest
	if !readJSON(w, r, &body) {
		return
	}

	found, limited, err := h.directory.Search(r.Context(), tenant.Scope(), caller.UserID,
		entity.DirectorySearch{Term: body.SearchTerm, Limit: body.Limit})
	if err != nil {
		switch {
		case errors.Is(err, entity.ErrDirectoryTermRequired):
			writeError(w, http.StatusBadRequest, codeMissingParam, err.Error())
		case errors.As(err, &validation.Errors{}):
			writeError(w, http.StatusBadRequest, codeInvalidParam, err.Error())
		default:
			writeInternal(r.Context(), w, "Could not search the directory", err)
		}
		return
	}

	results := make([]directoryResult, 0, len(found))
	for _, user := range found {
		results = append(results, directoryResult{
			UserID: user.UserID, DisplayName: user.DisplayName, AvatarURL: user.AvatarURL,
		})
	}
	writeJSON(w, http.StatusOK, directoryResponse{Results: results, Limited: limited})
}
