package dynamic

// TLSConfiguration contains the dynamic TLS configuration.
type TLSConfiguration struct {
	Certificates []*CertAndStores      `json:"certificates,omitempty" yaml:"certificates,omitempty"`
	Options      map[string]TLSOptions `json:"options,omitempty" yaml:"options,omitempty"`
	Stores       map[string]TLSStore   `json:"stores,omitempty" yaml:"stores,omitempty"`
}

// Certificate references a certificate and its key by file path.
type Certificate struct {
	CertFile string `json:"certFile,omitempty" yaml:"certFile,omitempty"`
	KeyFile  string `json:"keyFile,omitempty" yaml:"keyFile,omitempty"`
}

// CertAndStores is a certificate and the TLS stores it is added to.
type CertAndStores struct {
	CertFile string   `json:"certFile,omitempty" yaml:"certFile,omitempty"`
	KeyFile  string   `json:"keyFile,omitempty" yaml:"keyFile,omitempty"`
	Stores   []string `json:"stores,omitempty" yaml:"stores,omitempty"`
}

// TLSOptions are named TLS options that routers can reference.
type TLSOptions struct {
	MinVersion       string      `json:"minVersion,omitempty" yaml:"minVersion,omitempty"`
	MaxVersion       string      `json:"maxVersion,omitempty" yaml:"maxVersion,omitempty"`
	CipherSuites     []string    `json:"cipherSuites,omitempty" yaml:"cipherSuites,omitempty"`
	CurvePreferences []string    `json:"curvePreferences,omitempty" yaml:"curvePreferences,omitempty"`
	SniStrict        bool        `json:"sniStrict,omitempty" yaml:"sniStrict,omitempty"`
	ALPNProtocols    []string    `json:"alpnProtocols,omitempty" yaml:"alpnProtocols,omitempty"`
	ClientAuth       *ClientAuth `json:"clientAuth,omitempty" yaml:"clientAuth,omitempty" label:"allowEmpty"`
}

// ClientAuth configures client certificate verification.
type ClientAuth struct {
	CAFiles        []string `json:"caFiles,omitempty" yaml:"caFiles,omitempty"`
	ClientAuthType string   `json:"clientAuthType,omitempty" yaml:"clientAuthType,omitempty"`
}

// TLSStore is a TLS store, holding the default certificate used when no other matches.
type TLSStore struct {
	DefaultCertificate   *Certificate          `json:"defaultCertificate,omitempty" yaml:"defaultCertificate,omitempty"`
	DefaultGeneratedCert *DefaultGeneratedCert `json:"defaultGeneratedCert,omitempty" yaml:"defaultGeneratedCert,omitempty"`
}

// DefaultGeneratedCert asks a certificate resolver for the default certificate.
type DefaultGeneratedCert struct {
	Resolver string  `json:"resolver,omitempty" yaml:"resolver,omitempty"`
	Domain   *Domain `json:"domain,omitempty" yaml:"domain,omitempty"`
}
