package middleware

import "github.com/gin-gonic/gin"

type ConcurrencyScope string

const (
	ConcurrencyScopeUser   ConcurrencyScope = "user"
	ConcurrencyScopeAPIKey ConcurrencyScope = "api_key"
	ConcurrencyScopeGroup  ConcurrencyScope = "group"
)

// AuthSubject is the minimal authenticated identity stored in gin context.
type AuthSubject struct {
	UserID             int64
	APIKeyID           int64
	GroupID            int64
	Concurrency        int
	ConcurrencyScope   ConcurrencyScope
	ConcurrencyScopeID int64
}

func (s AuthSubject) ResolvedConcurrencyScope() (ConcurrencyScope, int64) {
	if s.ConcurrencyScope != "" && s.ConcurrencyScopeID > 0 {
		return s.ConcurrencyScope, s.ConcurrencyScopeID
	}
	return ConcurrencyScopeUser, s.UserID
}

func GetAuthSubjectFromContext(c *gin.Context) (AuthSubject, bool) {
	value, exists := c.Get(string(ContextKeyUser))
	if !exists {
		return AuthSubject{}, false
	}
	subject, ok := value.(AuthSubject)
	return subject, ok
}

func GetUserRoleFromContext(c *gin.Context) (string, bool) {
	value, exists := c.Get(string(ContextKeyUserRole))
	if !exists {
		return "", false
	}
	role, ok := value.(string)
	return role, ok
}
