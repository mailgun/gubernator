package httpapi

type Scope int

const (
	ScopePublic Scope = iota
	ScopeProtected
	ScopePrivate
)

var scopes = []string{
	"public",
	"protected",
	"private",
}

func (s Scope) String() string {
	return scopes[s]
}

func (s Scope) IsValid() bool {
	return s == ScopePublic || s == ScopeProtected || s == ScopePrivate
}
