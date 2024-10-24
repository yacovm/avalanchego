package tls

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"github.com/stretchr/testify/require"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"
)

func TestTLS(t *testing.T) {
	for i := 3; i <= 8; i++ {
		runTest(t, i)
	}
}

func runTest(t *testing.T, i int) {
	serverCert := makeTLSCert(t, i*1024)
	clientCert := makeTLSCert(t, i*1024)

	srvConfig := Config{
		ClientAuth:         RequireAnyClientCert,
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS13,
		Certificates:       []Certificate{serverCert},
		VerifyConnection: func(cs ConnectionState) error {
			pk := cs.PeerCertificates[0].PublicKey
			switch pk.(type) {
			case *rsa.PublicKey:
				n := pk.(*rsa.PublicKey).N.BitLen()
				fmt.Println("received RSA certificate of", n, "bits", "and exponent of", pk.(*rsa.PublicKey).E)
			default:
			}

			return nil
		},
	}

	clientConfig := Config{
		ClientAuth:         RequireAnyClientCert,
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS13,
		Certificates:       []Certificate{clientCert},
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	t.Log("Listening on", listener.Addr())

	var wg sync.WaitGroup
	wg.Add(1)

	msg := "hello world"

	go func() {
		defer wg.Done()
		conn, err := listener.Accept()
		require.NoError(t, err)

		tlsConn := Server(conn, &srvConfig)

		buff := make([]byte, 1024)
		byRead, err := tlsConn.Read(buff)
		require.NoError(t, err)

		require.Equal(t, msg, string(buff[:byRead]))
	}()

	conn, err := Dial("tcp", listener.Addr().String(), &clientConfig)
	require.NoError(t, err)

	_, err = conn.Write([]byte(msg))
	require.NoError(t, err)

	wg.Wait()
}

func makeTLSCert(t *testing.T, keySize int) Certificate {
	x509Cert := makeCert(t, keySize)

	rawX509PEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: x509Cert.Raw})
	privKeyDER, err := x509.MarshalPKCS8PrivateKey(x509Cert.PrivateKey)
	require.NoError(t, err)

	privKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privKeyDER})

	tlsCertServer, err := X509KeyPair(rawX509PEM, privKeyPEM)
	require.NoError(t, err)

	return tlsCertServer
}

type certKeyPair struct {
	x509.Certificate
	*rsa.PrivateKey
}

func makeCert(t *testing.T, keySize int) certKeyPair {
	privKey, err := rsa.GenerateKey(rand.Reader, keySize)
	require.NoError(t, err)

	template := x509Template()
	certBytes, err := x509.CreateCertificate(rand.Reader, template, template, &privKey.PublicKey, privKey)
	require.NoError(t, err)

	cert, err := x509.ParseCertificate(certBytes)
	require.NoError(t, err)

	return certKeyPair{
		Certificate: *cert,
		PrivateKey:  privKey,
	}
}

// default template for X509 certificates
func x509Template() *x509.Certificate {
	return &x509.Certificate{
		SerialNumber:          big.NewInt(0).SetInt64(100),
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour).UTC(),
		BasicConstraintsValid: true,
	}
}
