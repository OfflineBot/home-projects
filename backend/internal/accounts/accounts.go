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
	"github.com/offlinebot/home-projects/backend/internal/model"
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

	// A credential whose remote side locks is used once at a time: that is what
	// the reservation is for, and two schedulers racing for the same mailbox is
	// how an account gets locked out. A machine on the home network is the
	// other case entirely — a board can easily have a terminal, a second
	// terminal and a status check all signing in at the same moment, and
	// serialising those means the second one is told "another attempt is
	// already running" and simply does not open. Nothing is at risk there, so
	// nothing is queued.
	kind, known := capability.AccountKindByName(account.Kind)
	if known && !kind.Locks {
		return attemptFreely(ctx, env, account, timeout, fn)
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
		// A kind whose remote side locks pays the full price for a failed
		// attempt: the stored password goes. A kind that does not lock — a
		// machine on the home network, a key — keeps it, because there is
		// nothing to protect and everything to lose.
		if kind, ok := capability.AccountKindByName(account.Kind); ok && !kind.Locks {
			if rerr := st.ReleaseAttempt(ctx, accountID, reason); rerr != nil {
				return httpx.Internal("the attempt could not be closed").WithCause(rerr)
			}
			st.Audit(ctx, nil, "account.attempt_failed", account.Title, "", map[string]any{"reason": reason})
			return httpx.New(502, "attempt_failed", "%s", reason)
		}
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
	// Everything that can be answered without the password is answered first.
	if kind.Precheck != nil {
		if err := kind.Precheck(ctx, env, account); err != nil {
			return httpx.New(400, "precheck_failed",
				"%v. The stored password was not touched.", err)
		}
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

// attemptFreely is the path for a credential that cannot be locked out: the
// secret is read and used, as often as it is needed and by as many callers at
// once as there are. Success and failure are still recorded, so the account
// page says the same things about it as about any other.
func attemptFreely(ctx context.Context, env *capability.Env, account *model.Account,
	timeout time.Duration, fn func(secret []byte) error) error {
	sealed, err := env.Store.SecretOf(ctx, account.ID)
	if err != nil {
		return httpx.New(409, "credential_missing", "This account has no stored password. Enter it again.")
	}
	secret, err := env.Box.Open(sealed)
	if err != nil {
		return httpx.Internal("The stored password could not be decrypted.")
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
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
		_ = env.Store.NoteAccountError(ctx, account.ID, attemptErr.Error())
		return httpx.New(502, "attempt_failed", "%s", attemptErr.Error())
	}
	_ = env.Store.ConfirmSuccess(ctx, account.ID)
	return nil
}
