package common

import (
	"bufio"
	"fmt"
	"io"
)

// CheckKeyFileEnd skips over additional newlines at the end of a key file
// holding the given number of hex characters.
func CheckKeyFileEnd(r *bufio.Reader, hexChars int) error {
	for i := 0; ; i++ {
		b, err := r.ReadByte()
		switch {
		case err == io.EOF:
			return nil
		case err != nil:
			return err
		case b != '\n' && b != '\r':
			return fmt.Errorf("invalid character %q at end of key file", b)
		case i >= 2:
			return fmt.Errorf("key file too long, want %d hex characters", hexChars)
		}
	}
}

// ReadASCII reads into 'buf', stopping when the buffer is full or
// when a non-printable control character is encountered.
func ReadASCII(buf []byte, r *bufio.Reader) (n int, err error) {
	for ; n < len(buf); n++ {
		buf[n], err = r.ReadByte()
		switch {
		case err == io.EOF || buf[n] < '!':
			return n, nil
		case err != nil:
			return n, err
		}
	}
	return n, nil
}
