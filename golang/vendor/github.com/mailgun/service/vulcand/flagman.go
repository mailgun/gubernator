package vulcand

import (
	"fmt"
	"sync"
)

const (
	middlewareType = "flagman"
	middlewareID   = "flagman.auth"

	authTypeAccount = "account"
	authTypeDomain  = "domain"

	accountPrivateURL = "%v/v2/flagman/accounts/private"
	accountPublicURL  = "%v/v2/flagman/accounts/public"
	domainPrivateURL  = "%v/v2/flagman/domains/$1/private"
)

var (
	flagmanAPIBaseURL     = "http://localhost:9001"
	flagmanAPIBaseURLOnce sync.Once
)

func InitFlagmanAPIBaseURL(val string) {
	flagmanAPIBaseURLOnce.Do(func() {
		flagmanAPIBaseURL = val
	})
}

type Flagman struct {
	AuthType      string `json:"AuthType"`
	Capture       string `json:"Capture"`
	URL           string `json:"Url"`
	AllowDisabled bool   `json:"AllowDisabled"`
	UseCache      bool   `json:"UseCache"`
}

func (f Flagman) String() string {
	return fmt.Sprintf("Flagman(AuthType=%v, Capture=%v, URL=%v, AllowDisabled=%v, UseCache=%v)",
		f.AuthType, f.Capture, f.URL, f.AllowDisabled, f.UseCache)
}

type FlagmanOption func(*Flagman)

// WithCapture overrides `Capture` field of a Flagman Spec.
func WithCapture(capture string) FlagmanOption {
	return func(f *Flagman) {
		f.Capture = capture
	}
}

func NewAccountPrivateKeyAuth(options ...FlagmanOption) Middleware {
	return Middleware{
		Type:     middlewareType,
		ID:       middlewareID,
		Priority: DefaultMiddlewarePriority,
		Spec: flagmanSpecWithOptions(Flagman{
			AuthType:      authTypeAccount,
			Capture:       "",
			URL:           fmt.Sprintf(accountPrivateURL, flagmanAPIBaseURL),
			AllowDisabled: true,
			UseCache:      true,
		}, options),
	}
}

func NewAccountPublicKeyAuth(options ...FlagmanOption) Middleware {
	return Middleware{
		Type:     middlewareType,
		ID:       middlewareID,
		Priority: DefaultMiddlewarePriority,
		Spec: flagmanSpecWithOptions(Flagman{
			AuthType:      authTypeAccount,
			Capture:       "",
			URL:           fmt.Sprintf(accountPublicURL, flagmanAPIBaseURL),
			AllowDisabled: true,
			UseCache:      true,
		}, options),
	}
}

func NewDomainPrivateKeyAuth(options ...FlagmanOption) Middleware {
	return Middleware{
		Type:     middlewareType,
		ID:       middlewareID,
		Priority: DefaultMiddlewarePriority,
		Spec: flagmanSpecWithOptions(Flagman{
			AuthType:      authTypeDomain,
			Capture:       "/v[23]/([^/]+)",
			URL:           fmt.Sprintf(domainPrivateURL, flagmanAPIBaseURL),
			AllowDisabled: true,
			UseCache:      true,
		}, options),
	}
}

func flagmanSpecWithOptions(spec Flagman, options []FlagmanOption) Flagman {
	for _, f := range options {
		f(&spec)
	}

	return spec
}
