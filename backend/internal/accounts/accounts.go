// Package accounts holds the one operation that touches a stored credential:
// a single attempt, with the consequences the brief demands.
//
// There is no second entry point. Whether the attempt comes from a scheduler
// or from the "Test connection" button, it runs through the same reservation,
// and it ends either in a confirmed success or in a deleted password.
package accounts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/httpx"
	"github.com/offlinebot/home-projects/backend/internal/store"
)

// Attempt reserves the credential, hands it to fn exactly once, and then
// either confirms the success or deletes the secret.
//
// fn returning nil means an unambiguous "signed in". Everything else — a wrong
// password, a timeout, an abort, an answer we cannot interpret — counts as
// used up.
func Attempt(ctx context.Context, env *capability.Env, accountID uuid.UUID, timeout time.Duration, fn func(secret []byte) error) error {
	st := env.Store

	account, err := st.AccountByID(ctx, accountID)
	if err != nil {
		return httpx.NotFound("There is no such account.")
	}
	if account.NeedsSecret {
		return httpx.New(409, "credential_missing",
			"The password for %s was used up. Enter it again — there is no automatic second attempt.", account.Title)
	}

	sealed, err := st.ReserveAttempt(ctx, accountID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrAttemptInFlight):
			return httpx.Conflict("Another attempt on this account is already running.")
		case errors.Is(err, store.ErrNoSecret):
			return httpx.New(409, "credential_missing",
				"This account has no stored password. Enter it again.")
		}
		return httpx.Internal("the attempt could not be prepared").WithCause(err)
	}

	secret, err := env.Box.Open(sealed)
	if err != nil {
		_ = st.ConsumeSecret(ctx, accountID, "the stored password could not be decrypted with the configured key")
		return httpx.Internal("The stored password could not be decrypted. It was removed; enter it again.")
	}

	attemptCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		attemptCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	_ = attemptCtx

	var attemptErr error
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				attemptErr = fmt.Errorf("the attempt crashed: %v", rec)
			}
		}()
		attemptErr = fn(secret)
	}()

	if attemptErr != nil {
		reason := attemptErr.Error()
		if cerr := st.ConsumeSecret(ctx, accountID, reason); cerr != nil {
			return httpx.Internal("the credential could not be removed after a failed attempt").WithCause(cerr)
		}
		st.Audit(ctx, nil, "account.credential_consumed", account.Title, "", map[string]any{"reason": reason})
		return httpx.New(401, "credential_consumed",
			"The attempt failed (%s). The stored password was deleted and will not be tried again — enter it once more.", reason)
	}

	if err := st.ConfirmSuccess(ctx, accountID); err != nil {
		return httpx.Internal("the success could not be recorded").WithCause(err)
	}
	st.Audit(ctx, nil, "account.tested", account.Title, "", nil)
	return nil
}

// Test runs the account kind's own probe. It is announced in the UI as what it
// is: the same attempt, with the same consequences.
func Test(ctx context.Context, env *capability.Env, accountID uuid.UUID) error {
	account, err := env.Store.AccountByID(ctx, accountID)
	if err != nil {
		return httpx.NotFound("There is no such account.")
	}
	kind, ok := capability.AccountKindByName(account.Kind)
	if !ok {
		return httpx.BadRequest("No account of kind %q is installed any more.", account.Kind)
	}
	if kind.Test == nil {
		return httpx.BadRequest("This kind of account cannot be tested on its own.")
	}
	if kind.SecretLabel == "" {
		// Nothing secret involved, so nothing can be consumed.
		if err := kind.Test(ctx, env, account, nil); err != nil {
			return httpx.New(400, "test_failed", "%v", err)
		}
		return env.Store.ConfirmSuccess(ctx, accountID)
	}
	return Attempt(ctx, env, accountID, 90*time.Second, func(secret []byte) error {
		return kind.Test(ctx, env, account, secret)
	})
}
