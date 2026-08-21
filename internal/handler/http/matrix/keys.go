package matrix

import (
	"encoding/json"
	"net/http"

	"github.com/thelemail/thaumaste/internal/pkg/signing"
)

type verifyKey struct {
	Key string `json:"key"`
}

type oldVerifyKey struct {
	Key       string `json:"key"`
	ExpiredTS int64  `json:"expired_ts"`
}

type serverKeysResponse struct {
	ServerName    string                  `json:"server_name"`
	VerifyKeys    map[string]verifyKey    `json:"verify_keys"`
	OldVerifyKeys map[string]oldVerifyKey `json:"old_verify_keys"`
	ValidUntilTS  int64                   `json:"valid_until_ts"`
}

func (h *Handler) serverKeys(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantFrom(r.Context())
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "No server is configured for this host")
		return
	}

	keys, err := h.tenants.Keys(r.Context(), tenant.Scope())
	if err != nil {
		writeInternal(r.Context(), w, "Could not read the server keys", err)
		return
	}

	body := serverKeysResponse{
		ServerName:    tenant.ServerName,
		VerifyKeys:    map[string]verifyKey{},
		OldVerifyKeys: map[string]oldVerifyKey{},
		ValidUntilTS:  h.clock().UTC().Add(h.keyValidity).UnixMilli(),
	}
	for _, k := range keys {
		encoded := signing.EncodeKey(k.PublicKey)
		if k.Active() {
			body.VerifyKeys[k.KeyID] = verifyKey{Key: encoded}
			continue
		}
		body.OldVerifyKeys[k.KeyID] = oldVerifyKey{Key: encoded, ExpiredTS: k.ExpiredAt.UnixMilli()}
	}
	if len(body.VerifyKeys) == 0 {
		writeError(w, http.StatusInternalServerError, codeUnknown, "This server has no active signing key")
		return
	}

	unsigned, err := json.Marshal(body)
	if err != nil {
		writeInternal(r.Context(), w, "Could not encode the server keys", err)
		return
	}
	signed, err := h.tenants.SignAs(r.Context(), tenant.Scope(), unsigned)
	if err != nil {
		writeInternal(r.Context(), w, "Could not sign the server keys", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(signed)
}
