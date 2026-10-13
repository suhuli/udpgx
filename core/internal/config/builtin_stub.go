//go:build !builtinredis

package config

// Public source builds never embed a user's credential. Custom Redis works.
func builtinRedis() (Redis, bool) { return Redis{}, false }
