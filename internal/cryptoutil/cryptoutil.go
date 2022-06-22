package cryptoutil

import (
	"crypto"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"io"
	"os/exec"

	"github.com/pkg/errors"
	"github.com/smallstep/cli/internal/plugin"
	"go.step.sm/crypto/pemutil"
)

// CreateSigner reads a key from a file with a given name or creates a signer
// with the given kms and name uri.
func CreateSigner(kms, name string, opts ...pemutil.Options) (crypto.Signer, error) {
	if kms == "" {
		s, err := pemutil.Read(name, opts...)
		if err != nil {
			return nil, err
		}
		if sig, ok := s.(crypto.Signer); ok {
			return sig, nil
		}
		return nil, fmt.Errorf("file %s does not contain a valid private key", name)
	}

	return newKMSSigner(kms, name)
}

type kmsSigner struct {
	crypto.PublicKey
	name     string
	kms, key string
}

// exitError returns the error displayed on stderr after running the given
// command.
func exitError(cmd *exec.Cmd, err error) error {
	if e, ok := err.(*exec.ExitError); ok {
		return fmt.Errorf("command %q failed with:\n%s", cmd.String(), e.Stderr)
	}
	return fmt.Errorf("command %q failed with: %w", cmd.String(), err)
}

// newKMSSigner creates a signer using `step-kms-plugin` as the signer.
func newKMSSigner(kms, key string) (crypto.Signer, error) {
	name, err := plugin.LookPath("kms")
	if err != nil {
		return nil, err
	}

	args := []string{"key"}
	if kms != "" {
		args = append(args, "--kms", kms)
	}
	args = append(args, key)

	// Get public key
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, exitError(cmd, err)
	}

	pub, err := pemutil.Parse(out)
	if err != nil {
		return nil, err
	}

	return &kmsSigner{
		PublicKey: pub,
		name:      name,
		kms:       kms,
		key:       key,
	}, nil
}

// Public implements crypto.Signer and returns the public key.
func (s *kmsSigner) Public() crypto.PublicKey {
	return s.PublicKey
}

// Sign implements crypto.Signer using the `step-kms-plugin`.
func (s *kmsSigner) Sign(rand io.Reader, digest []byte, opts crypto.SignerOpts) (signature []byte, err error) {
	args := []string{"sign", "--format", "base64"}
	if s.kms != "" {
		args = append(args, "--kms", s.kms)
	}
	if _, ok := s.PublicKey.(*rsa.PublicKey); ok {
		if _, pss := opts.(*rsa.PSSOptions); pss {
			args = append(args, "--pss")
		}
		switch opts.HashFunc() {
		case crypto.SHA256:
			args = append(args, "--alg", "SHA256")
		case crypto.SHA384:
			args = append(args, "--alg", "SHA384")
		case crypto.SHA512:
			args = append(args, "--alg", "SHA512")
		default:
			return nil, errors.Errorf("unsupported hash function %q", opts.HashFunc().String())
		}
	}
	args = append(args, s.key)

	cmd := exec.Command(s.name, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	go func() {
		defer stdin.Close()
		stdin.Write(digest)
	}()
	out, err := cmd.Output()
	if err != nil {
		return nil, exitError(cmd, err)
	}
	return base64.StdEncoding.DecodeString(string(out))
}
