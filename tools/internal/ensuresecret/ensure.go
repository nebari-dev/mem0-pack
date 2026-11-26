// Package ensuresecret creates the pack's admin-key Secret once, in-cluster,
// using a ServiceAccount that may only create Secrets. An existing Secret is
// never read or changed.
package ensuresecret

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
)

const keyBytes = 32

// Config names the Secret to create and the data key that holds the value.
type Config struct {
	Namespace, Name, Key string
}

// SecretCreator creates a Secret. Create returns nil both when the Secret was
// created and when it already exists.
type SecretCreator interface {
	Create(ctx context.Context, ns, name, key string, value []byte) error
}

// Ensure generates a random key (32 bytes from random, hex-encoded to 64
// characters) and asks c to create the Secret with it.
func Ensure(ctx context.Context, c SecretCreator, cfg Config, random io.Reader) error {
	raw := make([]byte, keyBytes)
	if _, err := io.ReadFull(random, raw); err != nil {
		return fmt.Errorf("read random bytes: %w", err)
	}
	value := make([]byte, hex.EncodedLen(len(raw)))
	hex.Encode(value, raw)
	if err := c.Create(ctx, cfg.Namespace, cfg.Name, cfg.Key, value); err != nil {
		return fmt.Errorf("create secret %s/%s: %w", cfg.Namespace, cfg.Name, err)
	}
	return nil
}
