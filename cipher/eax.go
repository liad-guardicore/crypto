// Use of this source code is governed by a license
// that can be found in the LICENSE file.

// Package cipher implements additional block cipher modes
// that can be wrapped around low-level block cipher implementations.
// For standard block cipher modes see: https://golang.org/pkg/crypto/cipher
package cipher

import (
	"crypto/cipher"
	"crypto/subtle"
	"errors"
	"hash"

	"github.com/enceve/crypto"
	"github.com/enceve/crypto/cmac"
)

const (
	nTag = 0x0 // The nonce tag constant
	hTag = 0x1 // The additional data tag constant
	cTag = 0x2 // The ciphertext tag constant
)

// The EAX cipher
type eaxCipher struct {
	blockCipher cipher.Block
	ctr, block  []byte
	mac         hash.Hash
	size        int
}

// NewEAX returns a cipher.AEAD wrapping the cipher.Block.
// EAX is a two pass-scheme AEAD cipher with provable security.
// For authentication EAX uses CMac (OMAC1).
// The tagsize argument specifies the number of bytes of the auth. tag
// and must be between 1 and the block size of the cipher.
// This function returns a non-nil error if the given block cipher
// is not supported by CMac (see crypto/cmac for details)
func NewEAX(c cipher.Block, tagsize int) (cipher.AEAD, error) {
	m, err := cmac.New(c)
	if err != nil {
		return nil, err
	}
	if tagsize < 1 || tagsize > c.BlockSize() {
		return nil, errors.New("tagSize must between 1 and BlockSize() of the given cipher")
	}
	return &eaxCipher{
		blockCipher: c,
		mac:         m,
		ctr:         make([]byte, c.BlockSize()),
		block:       make([]byte, c.BlockSize()),
		size:        tagsize,
	}, nil
}

func (c *eaxCipher) NonceSize() int { return c.blockCipher.BlockSize() }

func (c *eaxCipher) Overhead() int { return c.size }

func (c *eaxCipher) Seal(dst, nonce, plaintext, additionalData []byte) []byte {
	if n := len(nonce); n != c.blockCipher.BlockSize() {
		panic(crypto.NonceSizeError(n))
	}
	if len(dst) < len(plaintext) {
		panic("dst buffer to small")
	}

	tag := make([]byte, c.mac.BlockSize())

	// process nonce
	tag[len(tag)-1] = nTag
	c.mac.Write(tag)
	c.mac.Write(nonce)
	authNonce := c.mac.Sum(nil)
	c.mac.Reset()

	// process additional data
	tag[len(tag)-1] = hTag
	c.mac.Write(tag)
	c.mac.Write(additionalData)
	authData := c.mac.Sum(nil)
	c.mac.Reset()

	// encrypt
	n := len(plaintext)
	copy(c.ctr, authNonce) // set the ctr-mode nonce
	c.ctrCrypt(dst, plaintext)

	// process ciphertext
	tag[len(tag)-1] = cTag
	c.mac.Write(tag)
	c.mac.Write(dst[:n])
	tag = c.mac.Sum(tag[:0])
	c.mac.Reset()

	for i := range tag {
		tag[i] ^= authData[i] ^ authNonce[i]
	}
	return append(dst[:n], tag[:c.size]...)
}

func (c *eaxCipher) Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error) {
	if n := len(nonce); n != c.blockCipher.BlockSize() {
		return nil, crypto.NonceSizeError(n)
	}
	if len(ciphertext) < c.size {
		return nil, crypto.AuthenticationError{}
	}
	if len(dst) < len(ciphertext)-c.mac.Size() {
		panic("dst buffer to small")
	}

	hash := ciphertext[len(ciphertext)-c.size:]
	ciphertext = ciphertext[:len(ciphertext)-c.size]

	tag := make([]byte, c.mac.BlockSize())

	// process nonce
	tag[len(tag)-1] = nTag
	c.mac.Write(tag)
	c.mac.Write(nonce)
	authNonce := c.mac.Sum(nil)
	c.mac.Reset()

	// process additional data
	tag[len(tag)-1] = hTag
	c.mac.Write(tag)
	c.mac.Write(additionalData)
	authData := c.mac.Sum(nil)
	c.mac.Reset()

	// process ciphertext
	tag[len(tag)-1] = cTag
	c.mac.Write(tag)
	c.mac.Write(ciphertext)
	tag = c.mac.Sum(tag[:0])
	c.mac.Reset()

	for i := range tag {
		tag[i] ^= authData[i] ^ authNonce[i]
	}

	if subtle.ConstantTimeCompare(tag[:c.size], hash) != 1 {
		return nil, crypto.AuthenticationError{}
	}

	// decrypt
	n := len(ciphertext)
	copy(c.ctr, authNonce) // set the ctr-mode nonce
	c.ctrCrypt(dst, ciphertext)

	return dst[:n], nil
}

// ctrCrypt encrypts the bytes in src with the CTR mode and writes
// the ciphertext into dst
func (c *eaxCipher) ctrCrypt(dst, src []byte) {
	length := len(src)
	bs := c.blockCipher.BlockSize()
	n := length & (^(length - bs))

	for i := 0; i < n; i += bs {
		j := i + bs
		c.blockCipher.Encrypt(c.block, c.ctr)
		crypto.XOR(dst[i:j], src[i:j], c.block)

		// Increment counter
		for k := len(c.ctr) - 1; k >= 0; k-- {
			c.ctr[k]++
			if c.ctr[k] != 0 {
				break
			}
		}
	}
	if n < length {
		c.blockCipher.Encrypt(c.block, c.ctr)
		crypto.XOR(dst[n:], src[n:], c.block)
	}
	// no reset of ctr needed - Seal or Open does this for us
}
