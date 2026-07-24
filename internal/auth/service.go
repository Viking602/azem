package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/Viking602/azem/internal/auth/chatgpt"
	"github.com/Viking602/azem/internal/auth/grok"
	"github.com/Viking602/azem/internal/store/sqlite/dbgen"
)

type Account struct {
	ID            string
	Provider      string
	Email         string
	DisplayName   string
	Plan          string
	CredentialRef string
	Status        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
type AccountStatusChange struct {
	Provider  string
	AccountID string
	Status    string
}

type StatusChangeCallback func(context.Context, AccountStatusChange)

type EntitlementError struct {
	Provider string
	Status   int
}

func (e EntitlementError) Error() string {
	return fmt.Sprintf("%s subscription does not permit this operation (HTTP %d)", e.Provider, e.Status)
}

type Service struct {
	db           *sql.DB
	store        CredentialStore
	chatgpt      *chatgpt.Client
	grok         *grok.Client
	httpClient   *http.Client
	streamClient *http.Client
	refresh      singleflight.Group
	statusMu     sync.RWMutex
	statusChange StatusChangeCallback
}

func NewService(db *sql.DB, store CredentialStore, chatgptClient *chatgpt.Client, grokClient *grok.Client) *Service {
	if chatgptClient == nil {
		chatgptClient = chatgpt.NewClient()
	}
	if grokClient == nil {
		grokClient = grok.NewClient()
	}
	streamTransport := http.DefaultTransport.(*http.Transport).Clone()
	streamTransport.ResponseHeaderTimeout = 30 * time.Second
	return &Service{
		db: db, store: store, chatgpt: chatgptClient, grok: grokClient,
		httpClient: &http.Client{Timeout: 30 * time.Second}, streamClient: &http.Client{Transport: streamTransport},
	}
}

func (s *Service) SetStatusChangeCallback(callback StatusChangeCallback) {
	s.statusMu.Lock()
	s.statusChange = callback
	s.statusMu.Unlock()
}

func (s *Service) LoginChatGPT(ctx context.Context, openURL func(string) error) (Account, error) {
	tokens, err := s.chatgpt.Login(ctx, openURL)
	if err != nil {
		return Account{}, err
	}
	return s.storeChatGPT(ctx, tokens)
}

func (s *Service) ImportChatGPT(ctx context.Context, path string) (Account, error) {
	tokens, err := chatgpt.ImportCodex(path)
	if err != nil {
		return Account{}, err
	}
	return s.storeChatGPT(ctx, tokens)
}

func (s *Service) ImportChatGPTKeyring(ctx context.Context, codexHome string) (Account, error) {
	tokens, err := chatgpt.ImportCodexKeyring(codexHome)
	if err != nil {
		return Account{}, err
	}
	return s.storeChatGPT(ctx, tokens)
}

func (s *Service) LoginGrok(ctx context.Context, notify func(grok.DeviceAuthorization) error) (Account, error) {
	tokens, err := s.grok.LoginDevice(ctx, notify)
	if err != nil {
		return Account{}, err
	}
	if tokens.ClientID == "" {
		tokens.ClientID = s.grok.ClientID
	}
	return s.storeGrok(ctx, tokens)
}

func (s *Service) ImportGrok(ctx context.Context, path string) (Account, error) {
	tokens, err := grok.Import(path)
	if err != nil {
		return Account{}, err
	}
	if tokens.ClientID == "" {
		tokens.ClientID = s.grok.ClientID
	}
	refreshed := false
	if tokens.AccessToken == "" {
		source := tokens
		client := s.grokClientFor(tokens.ClientID)
		discovery, err := client.Discover(ctx)
		if err != nil {
			return Account{}, fmt.Errorf("discover Grok OAuth for imported credential: %w", err)
		}
		tokens, err = client.Refresh(ctx, discovery, source.RefreshToken)
		if err != nil {
			return Account{}, fmt.Errorf("refresh imported Grok credential: %w", err)
		}
		tokens.ClientID = source.ClientID
		tokens.SourcePath = source.SourcePath
		tokens.SourceKey = source.SourceKey
		refreshed = true
		tokens.AccountID = firstNonEmpty(tokens.AccountID, source.AccountID)
		tokens.Email = firstNonEmpty(tokens.Email, source.Email)
		tokens.Plan = firstNonEmpty(tokens.Plan, source.Plan)
	}
	account, err := s.storeGrok(ctx, tokens)
	if err != nil {
		return Account{}, err
	}
	if refreshed {
		if err := grok.SyncImported(tokens); err != nil {
			return account, fmt.Errorf("sync refreshed Grok CLI credential: %w", err)
		}
	}
	return account, nil
}

func (s *Service) Credential(ctx context.Context, provider string, accountID string) (Credential, error) {
	credential, err := s.store.Get(ctx, provider, accountID)
	if err != nil {
		return Credential{}, err
	}
	if !credential.ExpiresAt.IsZero() && time.Until(credential.ExpiresAt) < time.Minute && credential.RefreshToken != "" {
		return s.Refresh(ctx, provider, accountID)
	}
	return credential, nil
}

func (s *Service) Accounts(ctx context.Context, provider string) ([]Account, error) {
	queries := dbgen.New(s.db)
	var rows []dbgen.Account
	var err error
	if provider != "" {
		rows, err = queries.ListAccountsByProvider(ctx, provider)
	} else {
		rows, err = queries.ListAccounts(ctx)
	}
	if err != nil {
		return nil, err
	}
	accounts := make([]Account, 0, len(rows))
	for _, row := range rows {
		accounts = append(accounts, accountFromDB(row))
	}
	return accounts, nil
}

func (s *Service) HasAnyAccount(ctx context.Context, provider string) (bool, error) {
	exists, err := dbgen.New(s.db).HasAnyAccount(ctx, provider)
	if err != nil {
		return false, err
	}
	return exists != 0, nil
}

// HasActiveChatGPTAccount is the authorization predicate for ChatGPT-only
// features. Every active ChatGPT account currently comes from OAuth or a
// Codex token import with an access token; API-key and PAT imports are not
// supported. If those auth kinds are added, this predicate must be narrowed.
func (s *Service) HasActiveChatGPTAccount(ctx context.Context) (bool, error) {
	active, err := dbgen.New(s.db).HasActiveChatGPTAccount(ctx)
	if err != nil {
		return false, fmt.Errorf("query active ChatGPT account: %w", err)
	}
	return active != 0, nil
}

func (s *Service) Refresh(ctx context.Context, provider string, accountID string) (Credential, error) {
	key := credentialKey(provider, accountID)
	value, err, _ := s.refresh.Do(key, func() (any, error) {
		credential, err := s.store.Get(ctx, provider, accountID)
		if err != nil {
			return Credential{}, err
		}
		if credential.RefreshToken == "" {
			return Credential{}, fmt.Errorf("%s account has no refresh token", provider)
		}
		var refreshedGrok *grok.Tokens
		switch provider {
		case "chatgpt":
			tokens, err := s.chatgpt.Refresh(ctx, credential.RefreshToken)
			if err != nil {
				s.markStatus(context.WithoutCancel(ctx), provider, accountID, "reauth_required")
				return Credential{}, err
			}
			applyChatGPTTokens(&credential, tokens)
		case "grok":
			client := s.grokClientFor(credential.OAuthClientID)
			discovery, err := client.Discover(ctx)
			if err != nil {
				return Credential{}, err
			}
			tokens, err := client.Refresh(ctx, discovery, credential.RefreshToken)
			if err != nil {
				s.markStatus(context.WithoutCancel(ctx), provider, accountID, "reauth_required")
				return Credential{}, err
			}
			tokens.ClientID = client.ClientID
			tokens.SourcePath = credential.SourcePath
			tokens.SourceKey = credential.SourceKey
			refreshedGrok = &tokens
			applyGrokTokens(&credential, tokens)
		default:
			return Credential{}, fmt.Errorf("unsupported provider %q", provider)
		}
		if _, err := s.store.Put(ctx, credential); err != nil {
			return Credential{}, err
		}
		if refreshedGrok != nil {
			if err := grok.SyncImported(*refreshedGrok); err != nil {
				return Credential{}, fmt.Errorf("sync refreshed Grok CLI credential: %w", err)
			}
		}
		if err := s.markStatus(ctx, provider, accountID, "active"); err != nil {
			return Credential{}, err
		}
		return credential, nil
	})
	if err != nil {
		return Credential{}, err
	}
	return value.(Credential), nil
}

func (s *Service) Logout(ctx context.Context, provider string, accountID string) error {
	credential, loadErr := s.store.Get(ctx, provider, accountID)
	if loadErr == nil {
		switch provider {
		case "chatgpt":
			_ = s.chatgpt.Revoke(ctx, firstNonEmpty(credential.RefreshToken, credential.AccessToken))
		case "grok":
			if discovery, err := s.grok.Discover(ctx); err == nil {
				_ = s.grok.Revoke(ctx, discovery, firstNonEmpty(credential.RefreshToken, credential.AccessToken))
			}
		}
	}
	deleteErr := s.store.Delete(context.WithoutCancel(ctx), provider, accountID)
	statusErr := s.markStatus(context.WithoutCancel(ctx), provider, accountID, "logged_out")
	return errors.Join(deleteErr, statusErr)
}

func (s *Service) DoWithRefresh(ctx context.Context, provider string, accountID string, build func(Credential) (*http.Request, error)) (*http.Response, error) {
	return s.doWithRefresh(ctx, s.httpClient, provider, accountID, build)
}

// DoStreamWithRefresh authenticates a streaming request without imposing a
// total response-body timeout. The caller's context remains the stream's
// lifetime and the transport still bounds response-header waits.
func (s *Service) DoStreamWithRefresh(ctx context.Context, provider string, accountID string, build func(Credential) (*http.Request, error)) (*http.Response, error) {
	return s.doWithRefresh(ctx, s.streamClient, provider, accountID, build)
}

func (s *Service) doWithRefresh(ctx context.Context, client *http.Client, provider string, accountID string, build func(Credential) (*http.Request, error)) (*http.Response, error) {
	credential, err := s.Credential(ctx, provider, accountID)
	if err != nil {
		return nil, err
	}
	for attempt := range 2 {
		request, err := build(credential)
		if err != nil {
			return nil, err
		}
		if provider == "grok" {
			if err := s.grok.ValidateResourceURL(request.URL.String()); err != nil {
				return nil, err
			}
		}
		request = request.WithContext(ctx)
		request.Header.Set("Authorization", "Bearer "+credential.AccessToken)
		if provider == "chatgpt" {
			request.Header.Set("ChatGPT-Account-ID", accountID)
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, err
		}
		if response.StatusCode == http.StatusForbidden {
			response.Body.Close()
			return nil, EntitlementError{Provider: provider, Status: response.StatusCode}
		}
		if response.StatusCode != http.StatusUnauthorized || attempt == 1 {
			return response, nil
		}
		response.Body.Close()
		credential, err = s.Refresh(ctx, provider, accountID)
		if err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("request retry exhausted")
}

func (s *Service) Account(ctx context.Context, provider string, accountID string) (Account, error) {
	row, err := dbgen.New(s.db).GetAccount(ctx, dbgen.GetAccountParams{ProviderID: provider, ID: accountID})
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, fmt.Errorf("account not found")
	}
	if err != nil {
		return Account{}, err
	}
	return accountFromDB(row), nil
}

func (s *Service) storeChatGPT(ctx context.Context, tokens chatgpt.Tokens) (Account, error) {
	credential := Credential{Provider: "chatgpt", AccountID: stableAccountID(tokens.AccountID, tokens.Email, tokens.AccessToken), AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken, IDToken: tokens.IDToken, TokenType: tokens.TokenType, ExpiresAt: tokens.ExpiresAt, Email: tokens.Email, Plan: tokens.Plan}
	return s.storeCredential(ctx, credential)
}

func (s *Service) storeGrok(ctx context.Context, tokens grok.Tokens) (Account, error) {
	credential := Credential{Provider: "grok", AccountID: stableAccountID(tokens.AccountID, tokens.Email, tokens.AccessToken), AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken, IDToken: tokens.IDToken, TokenType: tokens.TokenType, OAuthClientID: tokens.ClientID, SourcePath: tokens.SourcePath, SourceKey: tokens.SourceKey, ExpiresAt: tokens.ExpiresAt, Email: tokens.Email, Plan: tokens.Plan}
	return s.storeCredential(ctx, credential)
}

func (s *Service) storeCredential(ctx context.Context, credential Credential) (Account, error) {
	reference, err := s.store.Put(ctx, credential)
	if err != nil {
		return Account{}, err
	}
	now := time.Now().UTC()
	account := Account{ID: credential.AccountID, Provider: credential.Provider, Email: credential.Email, DisplayName: credential.DisplayName, Plan: credential.Plan, CredentialRef: reference, Status: "active", CreatedAt: now, UpdatedAt: now}
	err = dbgen.New(s.db).UpsertAccount(ctx, dbgen.UpsertAccountParams{ID: account.ID, ProviderID: account.Provider, Email: account.Email, DisplayName: account.DisplayName, Plan: account.Plan, CredentialRef: account.CredentialRef, Status: account.Status, CreatedAt: now.UnixNano(), UpdatedAt: now.UnixNano()})
	if err != nil {
		_ = s.store.Delete(context.WithoutCancel(ctx), credential.Provider, credential.AccountID)
		return Account{}, err
	}
	return account, nil
}

func (s *Service) markStatus(ctx context.Context, provider string, accountID string, status string) error {
	result, err := dbgen.New(s.db).UpdateAccountStatus(ctx, dbgen.UpdateAccountStatusParams{Status: status, UpdatedAt: time.Now().UTC().UnixNano(), ProviderID: provider, ID: accountID})
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return nil
	}
	s.statusMu.RLock()
	callback := s.statusChange
	s.statusMu.RUnlock()
	if callback != nil {
		callback(ctx, AccountStatusChange{Provider: provider, AccountID: accountID, Status: status})
	}
	return nil
}

func accountFromDB(row dbgen.Account) Account {
	return Account{ID: row.ID, Provider: row.ProviderID, Email: row.Email, DisplayName: row.DisplayName, Plan: row.Plan, CredentialRef: row.CredentialRef, Status: row.Status, CreatedAt: time.Unix(0, row.CreatedAt).UTC(), UpdatedAt: time.Unix(0, row.UpdatedAt).UTC()}
}

func applyChatGPTTokens(credential *Credential, tokens chatgpt.Tokens) {
	credential.AccessToken = tokens.AccessToken
	if tokens.RefreshToken != "" {
		credential.RefreshToken = tokens.RefreshToken
	}
	if tokens.IDToken != "" {
		credential.IDToken = tokens.IDToken
	}
	if tokens.TokenType != "" {
		credential.TokenType = tokens.TokenType
	}
	if !tokens.ExpiresAt.IsZero() {
		credential.ExpiresAt = tokens.ExpiresAt
	}
}

func applyGrokTokens(credential *Credential, tokens grok.Tokens) {
	credential.AccessToken = tokens.AccessToken
	if tokens.RefreshToken != "" {
		credential.RefreshToken = tokens.RefreshToken
	}
	if tokens.IDToken != "" {
		credential.IDToken = tokens.IDToken
	}
	if tokens.TokenType != "" {
		credential.TokenType = tokens.TokenType
	}
	if tokens.ClientID != "" {
		credential.OAuthClientID = tokens.ClientID
	}
	if !tokens.ExpiresAt.IsZero() {
		credential.ExpiresAt = tokens.ExpiresAt
	}
}

func (s *Service) grokClientFor(clientID string) grok.Client {
	client := *s.grok
	if clientID != "" {
		client.ClientID = clientID
	}
	return client
}

func stableAccountID(accountID string, email string, accessToken string) string {
	if accountID != "" {
		return accountID
	}
	if email != "" {
		return email
	}
	digest := sha256.Sum256([]byte(accessToken))
	return fmt.Sprintf("anonymous-%x", digest[:8])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
