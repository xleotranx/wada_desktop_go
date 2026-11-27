package pig

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/google/uuid"
	"wada-desktop-go/internal/protocol"
)

type providerLogin struct {
	cancel context.CancelFunc
	reply  chan string
}

// Providers lists registered AI model providers and their current credential state.
func (r *Runtime) Providers(ctx context.Context) ([]protocol.ProviderInfo, error) {
	e, release, err := r.acquire()
	defer release()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var list []protocol.ProviderInfo
	for _, pr := range e.models.GetProviders() {
		item := protocol.ProviderInfo{
			ID:             pr.ID,
			Name:           pr.Name,
			SupportsAPIKey: pr.Auth.APIKey != nil,
			SupportsOAuth:  pr.Auth.OAuth != nil,
		}
		if auth, err := e.models.CheckAuth(ctx, pr.ID); err == nil && auth != nil {
			item.Connected = true
			item.Source = string(auth.Source)
		}
		list = append(list, item)
	}
	slices.SortFunc(list, func(a, b protocol.ProviderInfo) int {
		if a.Connected != b.Connected {
			if a.Connected {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return list, nil
}

// StartProviderLogin begins an interactive provider authentication flow and streams
// prompts and status notices as EventProviderAuth events keyed by loginID.
func (r *Runtime) StartProviderLogin(ctx context.Context, req protocol.ProviderLoginStartRequest) (string, error) {
	e, release, err := r.acquire()
	if err != nil {
		release()
		return "", err
	}
	if err := ctx.Err(); err != nil {
		release()
		return "", err
	}
	if req.ProviderID == "" {
		release()
		return "", errors.New("providerId is required")
	}
	var authType ai.AuthType
	switch req.AuthType {
	case protocol.ProviderAuthAPIKey:
		authType = ai.CredentialAPIKey
	case protocol.ProviderAuthOAuth:
		authType = ai.CredentialOAuth
	default:
		release()
		return "", errors.New("unsupported provider auth type")
	}

	loginID := req.LoginID
	if loginID == "" {
		loginID = uuid.NewString()
	}
	loginCtx, cancel := context.WithCancel(e.ctx)
	state := &providerLogin{cancel: cancel}

	e.loginsMu.Lock()
	if _, exists := e.logins[loginID]; exists {
		e.loginsMu.Unlock()
		cancel()
		release()
		return "", errors.New("login session already active")
	}
	e.logins[loginID] = state
	e.loginsMu.Unlock()

	if !e.spawn(func(_ context.Context) {
		defer release()
		defer func() {
			cancel()
			e.loginsMu.Lock()
			if cur := e.logins[loginID]; cur == state {
				delete(e.logins, loginID)
			}
			e.loginsMu.Unlock()
		}()
		interaction := ai.AuthInteraction{
			Prompt: func(pctx context.Context, prompt ai.AuthPrompt) (string, error) {
				reply := make(chan string, 1)
				e.loginsMu.Lock()
				state.reply = reply
				e.loginsMu.Unlock()
				defer func() {
					e.loginsMu.Lock()
					if state.reply == reply {
						state.reply = nil
					}
					e.loginsMu.Unlock()
				}()

				ev := protocol.ProviderAuthEvent{
					LoginID:    loginID,
					ProviderID: req.ProviderID,
				}
				switch p := prompt.(type) {
				case ai.AuthTextPrompt:
					ev.Kind = protocol.ProviderAuthPromptText
					ev.Message = p.Message
				case ai.AuthSecretPrompt:
					ev.Kind = protocol.ProviderAuthPromptSecret
					ev.Message = p.Message
				case ai.AuthManualCodePrompt:
					ev.Kind = protocol.ProviderAuthPromptCode
					ev.Message = p.Message
				case ai.AuthSelectPrompt:
					ev.Kind = protocol.ProviderAuthPromptSelect
					ev.Message = p.Message
					for _, opt := range p.Options {
						ev.Options = append(ev.Options, protocol.ProviderSelectOption{
							ID:    opt.ID,
							Label: opt.Label,
						})
					}
				}
				e.emit(protocol.Event{
					Kind:         protocol.EventProviderAuth,
					ProviderAuth: &ev,
				})

				select {
				case ans := <-reply:
					return ans, nil
				case <-pctx.Done():
					return "", pctx.Err()
				case <-loginCtx.Done():
					return "", context.Canceled
				}
			},
			Notify: func(event ai.AuthEvent) {
				ev := protocol.ProviderAuthEvent{
					LoginID:    loginID,
					ProviderID: req.ProviderID,
				}
				switch n := event.(type) {
				case ai.AuthURLEvent:
					ev.Kind = protocol.ProviderAuthURL
					ev.URL = n.URL
					ev.Message = n.Instructions
				case ai.AuthDeviceCodeEvent:
					ev.Kind = protocol.ProviderAuthDeviceCode
					ev.URL = n.VerificationURI
					ev.UserCode = n.UserCode
					ev.Message = "Enter code " + n.UserCode
				case ai.AuthInfoEvent:
					ev.Kind = protocol.ProviderAuthInfo
					ev.Message = n.Message
				case ai.AuthProgressEvent:
					ev.Kind = protocol.ProviderAuthProgress
					ev.Message = n.Message
				default:
					return
				}
				e.emit(protocol.Event{
					Kind:         protocol.EventProviderAuth,
					ProviderAuth: &ev,
				})
			},
		}

		_, loginErr := e.models.Login(loginCtx, req.ProviderID, authType, interaction)
		if loginErr == nil {
			e.emit(protocol.Event{
				Kind: protocol.EventProviderAuth,
				ProviderAuth: &protocol.ProviderAuthEvent{
					LoginID:    loginID,
					ProviderID: req.ProviderID,
					Kind:       protocol.ProviderAuthDone,
				},
			})
			e.emit(protocol.Event{Kind: protocol.EventModelsChanged})
			return
		}
		if !errors.Is(loginErr, context.Canceled) {
			e.emit(protocol.Event{
				Kind: protocol.EventProviderAuth,
				ProviderAuth: &protocol.ProviderAuthEvent{
					LoginID:    loginID,
					ProviderID: req.ProviderID,
					Kind:       protocol.ProviderAuthError,
					Message:    loginErr.Error(),
				},
			})
		}
	}) {
		e.loginsMu.Lock()
		delete(e.logins, loginID)
		e.loginsMu.Unlock()
		cancel()
		release()
		return "", ErrNotConnected
	}

	return loginID, nil
}

// AnswerProviderLogin delivers user input to a waiting provider login prompt.
func (r *Runtime) AnswerProviderLogin(ctx context.Context, loginID, value string) error {
	e, release, err := r.acquire()
	defer release()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e.loginsMu.Lock()
	state := e.logins[loginID]
	var ch chan string
	if state != nil {
		ch = state.reply
	}
	e.loginsMu.Unlock()
	if state == nil || ch == nil {
		return errors.New("provider login prompt is not active")
	}
	select {
	case ch <- value:
		return nil
	default:
		return errors.New("provider login prompt already answered")
	}
}

// CancelProviderLogin aborts an in-flight provider login session.
func (r *Runtime) CancelProviderLogin(ctx context.Context, loginID string) error {
	e, release, err := r.acquire()
	defer release()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e.loginsMu.Lock()
	state := e.logins[loginID]
	if state != nil {
		delete(e.logins, loginID)
	}
	e.loginsMu.Unlock()
	if state != nil {
		state.cancel()
	}
	return nil
}

// LogoutProvider disconnects a provider's stored credentials.
func (r *Runtime) LogoutProvider(ctx context.Context, providerID string) error {
	e, release, err := r.acquire()
	defer release()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := e.models.Logout(ctx, providerID); err != nil {
		return err
	}
	e.emit(protocol.Event{Kind: protocol.EventModelsChanged})
	return nil
}
