// Package socks implements just enough of SOCKS5 (RFC 1928) to let a
// browser send its connections through poof's tunnel. Only the CONNECT
// command and the no-auth method are supported — that's all a browser
// needs, and keeping it minimal means we own the hostname handling that
// makes DNS resolve through the tunnel instead of locally.
package socks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// Dialer is the one thing the proxy needs: a way to open a connection
// that egresses from the Exit. tunnel.Netstack satisfies this.
type Dialer interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
}

// SOCKS5 protocol constants (RFC 1928).
const (
	version5 = 0x05

	methodNoAuth       = 0x00
	methodNoAcceptable = 0xFF

	cmdConnect = 0x01

	atypIPv4   = 0x01
	atypDomain = 0x03
	atypIPv6   = 0x04

	repSuccess           = 0x00
	repGeneralFailure    = 0x01
	repCommandNotSupport = 0x07
)

// Server accepts SOCKS5 clients on a local listener and relays each
// CONNECT through the provided Dialer.
type Server struct {
	dialer Dialer
}

func NewServer(d Dialer) *Server { return &Server{dialer: d} }

// ListenAndServe serves until the listener is closed or ctx is done.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("socks: listen %s: %w", addr, err)
	}
	// Unblock Accept when the session ends.
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil // clean shutdown
			}
			return fmt.Errorf("socks: accept: %w", err)
		}
		go s.handle(ctx, conn)
	}
}

func (s *Server) handle(ctx context.Context, client net.Conn) {
	defer client.Close()
	if err := s.negotiate(client); err != nil {
		return // malformed client; just drop it
	}
	target, err := s.readRequest(client)
	if err != nil {
		return
	}

	// THE line that matters: dial through the tunnel, not the OS. A
	// hostname target is passed through verbatim so netstack resolves
	// it inside the tunnel (no local DNS leak).
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	remote, err := s.dialer.DialContext(dialCtx, "tcp", target)
	if err != nil {
		reply(client, repGeneralFailure)
		return
	}
	defer remote.Close()

	if err := reply(client, repSuccess); err != nil {
		return
	}
	relay(client, remote)
}

// negotiate performs the SOCKS5 greeting: read the client's method list,
// answer no-auth (or reject).
func (s *Server) negotiate(client net.Conn) error {
	// VER, NMETHODS, then NMETHODS bytes.
	header := make([]byte, 2)
	if _, err := io.ReadFull(client, header); err != nil {
		return err
	}
	if header[0] != version5 {
		return errors.New("socks: not version 5")
	}
	methods := make([]byte, header[1])
	if _, err := io.ReadFull(client, methods); err != nil {
		return err
	}
	for _, m := range methods {
		if m == methodNoAuth {
			_, err := client.Write([]byte{version5, methodNoAuth})
			return err
		}
	}
	client.Write([]byte{version5, methodNoAcceptable})
	return errors.New("socks: client offered no supported auth method")
}

// readRequest parses a CONNECT request and returns "host:port". The host
// is left as a hostname when ATYP=domain so it resolves through the
// tunnel.
func (s *Server) readRequest(client net.Conn) (string, error) {
	// VER, CMD, RSV, ATYP
	header := make([]byte, 4)
	if _, err := io.ReadFull(client, header); err != nil {
		return "", err
	}
	if header[0] != version5 {
		return "", errors.New("socks: bad request version")
	}
	if header[1] != cmdConnect {
		reply(client, repCommandNotSupport)
		return "", errors.New("socks: only CONNECT supported")
	}

	var host string
	switch header[3] {
	case atypIPv4:
		buf := make([]byte, 4)
		if _, err := io.ReadFull(client, buf); err != nil {
			return "", err
		}
		host = net.IP(buf).String()
	case atypIPv6:
		buf := make([]byte, 16)
		if _, err := io.ReadFull(client, buf); err != nil {
			return "", err
		}
		host = net.IP(buf).String()
	case atypDomain:
		lenByte := make([]byte, 1)
		if _, err := io.ReadFull(client, lenByte); err != nil {
			return "", err
		}
		name := make([]byte, lenByte[0])
		if _, err := io.ReadFull(client, name); err != nil {
			return "", err
		}
		host = string(name) // pass through — do NOT resolve locally
	default:
		reply(client, repGeneralFailure)
		return "", errors.New("socks: unknown address type")
	}

	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(client, portBuf); err != nil {
		return "", err
	}
	port := int(portBuf[0])<<8 | int(portBuf[1])
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

// reply sends a SOCKS5 reply with the given status. The bound-address
// fields are zeroed; browsers don't use them for CONNECT.
func reply(client net.Conn, status byte) error {
	_, err := client.Write([]byte{
		version5, status, 0x00, atypIPv4,
		0, 0, 0, 0, // BND.ADDR
		0, 0, // BND.PORT
	})
	return err
}

// relay splices two connections until either closes.
func relay(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		io.Copy(dst, src)
		// Wake the other direction: unblock its Read.
		if c, ok := dst.(interface{ CloseWrite() error }); ok {
			c.CloseWrite()
		}
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	<-done
}
