package dynamic

// Middleware is an HTTP middleware. Only one of its fields is normally set.
//
// The most common middlewares are modelled with typed fields. Less common ones
// (digestAuth, contentType, ipWhiteList) and the plugin middleware are modelled
// with generic maps so that they round-trip through YAML and JSON and still
// accept label input.
type Middleware struct {
	AddPrefix         *AddPrefix                `json:"addPrefix,omitempty" yaml:"addPrefix,omitempty"`
	StripPrefix       *StripPrefix              `json:"stripPrefix,omitempty" yaml:"stripPrefix,omitempty"`
	StripPrefixRegex  *StripPrefixRegex         `json:"stripPrefixRegex,omitempty" yaml:"stripPrefixRegex,omitempty"`
	ReplacePath       *ReplacePath              `json:"replacePath,omitempty" yaml:"replacePath,omitempty"`
	ReplacePathRegex  *ReplacePathRegex         `json:"replacePathRegex,omitempty" yaml:"replacePathRegex,omitempty"`
	RedirectScheme    *RedirectScheme           `json:"redirectScheme,omitempty" yaml:"redirectScheme,omitempty"`
	RedirectRegex     *RedirectRegex            `json:"redirectRegex,omitempty" yaml:"redirectRegex,omitempty"`
	Headers           *Headers                  `json:"headers,omitempty" yaml:"headers,omitempty"`
	BasicAuth         *BasicAuth                `json:"basicAuth,omitempty" yaml:"basicAuth,omitempty"`
	DigestAuth        map[string]any            `json:"digestAuth,omitempty" yaml:"digestAuth,omitempty"`
	ForwardAuth       *ForwardAuth              `json:"forwardAuth,omitempty" yaml:"forwardAuth,omitempty"`
	RateLimit         *RateLimit                `json:"rateLimit,omitempty" yaml:"rateLimit,omitempty"`
	Compress          *Compress                 `json:"compress,omitempty" yaml:"compress,omitempty" label:"allowEmpty"`
	Chain             *Chain                    `json:"chain,omitempty" yaml:"chain,omitempty"`
	IPAllowList       *IPAllowList              `json:"ipAllowList,omitempty" yaml:"ipAllowList,omitempty"`
	IPWhiteList       map[string]any            `json:"ipWhiteList,omitempty" yaml:"ipWhiteList,omitempty"`
	Retry             *Retry                    `json:"retry,omitempty" yaml:"retry,omitempty"`
	CircuitBreaker    *CircuitBreaker           `json:"circuitBreaker,omitempty" yaml:"circuitBreaker,omitempty"`
	Buffering         *Buffering                `json:"buffering,omitempty" yaml:"buffering,omitempty"`
	InFlightReq       *InFlightReq              `json:"inFlightReq,omitempty" yaml:"inFlightReq,omitempty"`
	Errors            *ErrorPage                `json:"errors,omitempty" yaml:"errors,omitempty"`
	ContentType       map[string]any            `json:"contentType,omitempty" yaml:"contentType,omitempty"`
	PassTLSClientCert *PassTLSClientCert        `json:"passTLSClientCert,omitempty" yaml:"passTLSClientCert,omitempty"`
	Plugin            map[string]map[string]any `json:"plugin,omitempty" yaml:"plugin,omitempty"`
}

// AddPrefix prepends a prefix to the request path.
type AddPrefix struct {
	Prefix string `json:"prefix,omitempty" yaml:"prefix,omitempty"`
}

// StripPrefix removes the first matching prefix from the request path.
type StripPrefix struct {
	Prefixes   []string `json:"prefixes,omitempty" yaml:"prefixes,omitempty"`
	ForceSlash bool     `json:"forceSlash,omitempty" yaml:"forceSlash,omitempty"`
}

// StripPrefixRegex removes the first matching regex from the request path.
type StripPrefixRegex struct {
	Regex []string `json:"regex,omitempty" yaml:"regex,omitempty"`
}

// ReplacePath replaces the request path.
type ReplacePath struct {
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
}

// ReplacePathRegex replaces the request path using a regex.
type ReplacePathRegex struct {
	Regex       string `json:"regex,omitempty" yaml:"regex,omitempty"`
	Replacement string `json:"replacement,omitempty" yaml:"replacement,omitempty"`
}

// RedirectScheme redirects requests to another scheme.
type RedirectScheme struct {
	Scheme    string `json:"scheme,omitempty" yaml:"scheme,omitempty"`
	Port      string `json:"port,omitempty" yaml:"port,omitempty"`
	Permanent bool   `json:"permanent,omitempty" yaml:"permanent,omitempty"`
}

// RedirectRegex redirects requests whose URL matches a regex.
type RedirectRegex struct {
	Regex       string `json:"regex,omitempty" yaml:"regex,omitempty"`
	Replacement string `json:"replacement,omitempty" yaml:"replacement,omitempty"`
	Permanent   bool   `json:"permanent,omitempty" yaml:"permanent,omitempty"`
}

// Headers manipulates request and response headers and sets security headers.
type Headers struct {
	CustomRequestHeaders              map[string]string `json:"customRequestHeaders,omitempty" yaml:"customRequestHeaders,omitempty"`
	CustomResponseHeaders             map[string]string `json:"customResponseHeaders,omitempty" yaml:"customResponseHeaders,omitempty"`
	AccessControlAllowCredentials     bool              `json:"accessControlAllowCredentials,omitempty" yaml:"accessControlAllowCredentials,omitempty"`
	AccessControlAllowHeaders         []string          `json:"accessControlAllowHeaders,omitempty" yaml:"accessControlAllowHeaders,omitempty"`
	AccessControlAllowMethods         []string          `json:"accessControlAllowMethods,omitempty" yaml:"accessControlAllowMethods,omitempty"`
	AccessControlAllowOriginList      []string          `json:"accessControlAllowOriginList,omitempty" yaml:"accessControlAllowOriginList,omitempty"`
	AccessControlAllowOriginListRegex []string          `json:"accessControlAllowOriginListRegex,omitempty" yaml:"accessControlAllowOriginListRegex,omitempty"`
	AccessControlExposeHeaders        []string          `json:"accessControlExposeHeaders,omitempty" yaml:"accessControlExposeHeaders,omitempty"`
	AccessControlMaxAge               int64             `json:"accessControlMaxAge,omitempty" yaml:"accessControlMaxAge,omitempty"`
	AddVaryHeader                     bool              `json:"addVaryHeader,omitempty" yaml:"addVaryHeader,omitempty"`
	AllowedHosts                      []string          `json:"allowedHosts,omitempty" yaml:"allowedHosts,omitempty"`
	HostsProxyHeaders                 []string          `json:"hostsProxyHeaders,omitempty" yaml:"hostsProxyHeaders,omitempty"`
	SSLRedirect                       bool              `json:"sslRedirect,omitempty" yaml:"sslRedirect,omitempty"`
	SSLTemporaryRedirect              bool              `json:"sslTemporaryRedirect,omitempty" yaml:"sslTemporaryRedirect,omitempty"`
	SSLHost                           string            `json:"sslHost,omitempty" yaml:"sslHost,omitempty"`
	SSLProxyHeaders                   map[string]string `json:"sslProxyHeaders,omitempty" yaml:"sslProxyHeaders,omitempty"`
	SSLForceHost                      bool              `json:"sslForceHost,omitempty" yaml:"sslForceHost,omitempty"`
	STSSeconds                        int64             `json:"stsSeconds,omitempty" yaml:"stsSeconds,omitempty"`
	STSIncludeSubdomains              bool              `json:"stsIncludeSubdomains,omitempty" yaml:"stsIncludeSubdomains,omitempty"`
	STSPreload                        bool              `json:"stsPreload,omitempty" yaml:"stsPreload,omitempty"`
	ForceSTSHeader                    bool              `json:"forceSTSHeader,omitempty" yaml:"forceSTSHeader,omitempty"`
	FrameDeny                         bool              `json:"frameDeny,omitempty" yaml:"frameDeny,omitempty"`
	CustomFrameOptionsValue           string            `json:"customFrameOptionsValue,omitempty" yaml:"customFrameOptionsValue,omitempty"`
	ContentTypeNosniff                bool              `json:"contentTypeNosniff,omitempty" yaml:"contentTypeNosniff,omitempty"`
	BrowserXSSFilter                  bool              `json:"browserXssFilter,omitempty" yaml:"browserXssFilter,omitempty"`
	CustomBrowserXSSValue             string            `json:"customBrowserXSSValue,omitempty" yaml:"customBrowserXSSValue,omitempty"`
	ContentSecurityPolicy             string            `json:"contentSecurityPolicy,omitempty" yaml:"contentSecurityPolicy,omitempty"`
	ContentSecurityPolicyReportOnly   string            `json:"contentSecurityPolicyReportOnly,omitempty" yaml:"contentSecurityPolicyReportOnly,omitempty"`
	PublicKey                         string            `json:"publicKey,omitempty" yaml:"publicKey,omitempty"`
	ReferrerPolicy                    string            `json:"referrerPolicy,omitempty" yaml:"referrerPolicy,omitempty"`
	PermissionsPolicy                 string            `json:"permissionsPolicy,omitempty" yaml:"permissionsPolicy,omitempty"`
	IsDevelopment                     bool              `json:"isDevelopment,omitempty" yaml:"isDevelopment,omitempty"`
}

// BasicAuth protects a route with HTTP basic authentication.
type BasicAuth struct {
	Users        []string `json:"users,omitempty" yaml:"users,omitempty"`
	UsersFile    string   `json:"usersFile,omitempty" yaml:"usersFile,omitempty"`
	Realm        string   `json:"realm,omitempty" yaml:"realm,omitempty"`
	RemoveHeader bool     `json:"removeHeader,omitempty" yaml:"removeHeader,omitempty"`
	HeaderField  string   `json:"headerField,omitempty" yaml:"headerField,omitempty"`
}

// ForwardAuth delegates authentication to an external service.
type ForwardAuth struct {
	Address                  string     `json:"address,omitempty" yaml:"address,omitempty"`
	TLS                      *ClientTLS `json:"tls,omitempty" yaml:"tls,omitempty"`
	TrustForwardHeader       bool       `json:"trustForwardHeader,omitempty" yaml:"trustForwardHeader,omitempty"`
	AuthResponseHeaders      []string   `json:"authResponseHeaders,omitempty" yaml:"authResponseHeaders,omitempty"`
	AuthResponseHeadersRegex string     `json:"authResponseHeadersRegex,omitempty" yaml:"authResponseHeadersRegex,omitempty"`
	AuthRequestHeaders       []string   `json:"authRequestHeaders,omitempty" yaml:"authRequestHeaders,omitempty"`
	AddAuthCookiesToResponse []string   `json:"addAuthCookiesToResponse,omitempty" yaml:"addAuthCookiesToResponse,omitempty"`
	HeaderField              string     `json:"headerField,omitempty" yaml:"headerField,omitempty"`
	ForwardBody              bool       `json:"forwardBody,omitempty" yaml:"forwardBody,omitempty"`
}

// ClientTLS holds client TLS settings for outgoing connections made by a middleware.
type ClientTLS struct {
	CA                 string `json:"ca,omitempty" yaml:"ca,omitempty"`
	Cert               string `json:"cert,omitempty" yaml:"cert,omitempty"`
	Key                string `json:"key,omitempty" yaml:"key,omitempty"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify,omitempty" yaml:"insecureSkipVerify,omitempty"`
}

// RateLimit limits the request rate.
type RateLimit struct {
	Average         int64            `json:"average,omitempty" yaml:"average,omitempty"`
	Period          string           `json:"period,omitempty" yaml:"period,omitempty"`
	Burst           int64            `json:"burst,omitempty" yaml:"burst,omitempty"`
	SourceCriterion *SourceCriterion `json:"sourceCriterion,omitempty" yaml:"sourceCriterion,omitempty"`
}

// SourceCriterion selects how the client of a request is identified.
type SourceCriterion struct {
	IPStrategy        *IPStrategy `json:"ipStrategy,omitempty" yaml:"ipStrategy,omitempty"`
	RequestHeaderName string      `json:"requestHeaderName,omitempty" yaml:"requestHeaderName,omitempty"`
	RequestHost       bool        `json:"requestHost,omitempty" yaml:"requestHost,omitempty"`
}

// IPStrategy selects the client IP from X-Forwarded-For.
type IPStrategy struct {
	Depth       int      `json:"depth,omitempty" yaml:"depth,omitempty"`
	ExcludedIPs []string `json:"excludedIPs,omitempty" yaml:"excludedIPs,omitempty"`
}

// Compress enables response compression.
type Compress struct {
	ExcludedContentTypes []string `json:"excludedContentTypes,omitempty" yaml:"excludedContentTypes,omitempty"`
	MinResponseBodyBytes int      `json:"minResponseBodyBytes,omitempty" yaml:"minResponseBodyBytes,omitempty"`
}

// Chain groups other middlewares.
type Chain struct {
	Middlewares []string `json:"middlewares,omitempty" yaml:"middlewares,omitempty"`
}

// IPAllowList restricts access to a set of client IP ranges.
type IPAllowList struct {
	SourceRange []string    `json:"sourceRange,omitempty" yaml:"sourceRange,omitempty"`
	IPStrategy  *IPStrategy `json:"ipStrategy,omitempty" yaml:"ipStrategy,omitempty"`
}

// Retry retries failed requests.
type Retry struct {
	Attempts        int    `json:"attempts,omitempty" yaml:"attempts,omitempty"`
	InitialInterval string `json:"initialInterval,omitempty" yaml:"initialInterval,omitempty"`
}

// CircuitBreaker stops sending requests to unhealthy services.
type CircuitBreaker struct {
	Expression       string `json:"expression,omitempty" yaml:"expression,omitempty"`
	CheckPeriod      string `json:"checkPeriod,omitempty" yaml:"checkPeriod,omitempty"`
	FallbackDuration string `json:"fallbackDuration,omitempty" yaml:"fallbackDuration,omitempty"`
	RecoveryDuration string `json:"recoveryDuration,omitempty" yaml:"recoveryDuration,omitempty"`
}

// Buffering buffers request and response bodies.
type Buffering struct {
	MaxRequestBodyBytes  int64  `json:"maxRequestBodyBytes,omitempty" yaml:"maxRequestBodyBytes,omitempty"`
	MemRequestBodyBytes  int64  `json:"memRequestBodyBytes,omitempty" yaml:"memRequestBodyBytes,omitempty"`
	MaxResponseBodyBytes int64  `json:"maxResponseBodyBytes,omitempty" yaml:"maxResponseBodyBytes,omitempty"`
	MemResponseBodyBytes int64  `json:"memResponseBodyBytes,omitempty" yaml:"memResponseBodyBytes,omitempty"`
	RetryExpression      string `json:"retryExpression,omitempty" yaml:"retryExpression,omitempty"`
}

// InFlightReq limits the number of concurrent requests.
type InFlightReq struct {
	Amount          int64            `json:"amount,omitempty" yaml:"amount,omitempty"`
	SourceCriterion *SourceCriterion `json:"sourceCriterion,omitempty" yaml:"sourceCriterion,omitempty"`
}

// ErrorPage serves custom pages for error status codes.
type ErrorPage struct {
	Status  []string `json:"status,omitempty" yaml:"status,omitempty"`
	Service string   `json:"service,omitempty" yaml:"service,omitempty"`
	Query   string   `json:"query,omitempty" yaml:"query,omitempty"`
}

// PassTLSClientCert passes client certificate information to the backend.
type PassTLSClientCert struct {
	PEM  bool           `json:"pem,omitempty" yaml:"pem,omitempty"`
	Info map[string]any `json:"info,omitempty" yaml:"info,omitempty"`
}
