package config

// SecurityConfig contém as configurações de segurança da aplicação
type SecurityConfig struct {
	JWT struct {
		Secret        string `json:"secret"`
		ExpirationHrs int    `json:"expiration_hours"`
	} `json:"jwt"`

	RateLimit struct {
		Enabled     bool `json:"enabled"`
		RequestsMax int  `json:"requests_max"`
		WindowSecs  int  `json:"window_seconds"`
	} `json:"rate_limit"`

	CORS struct {
		AllowOrigins     []string `json:"allow_origins"`
		AllowCredentials bool     `json:"allow_credentials"`
		AllowMethods     []string `json:"allow_methods"`
		AllowHeaders     []string `json:"allow_headers"`
		ExposeHeaders    []string `json:"expose_headers"`
		MaxAge           int      `json:"max_age"`
	} `json:"cors"`

	PasswordPolicy struct {
		MinLength      int  `json:"min_length"`
		RequireUpper   bool `json:"require_upper"`
		RequireLower   bool `json:"require_lower"`
		RequireNumber  bool `json:"require_number"`
		RequireSpecial bool `json:"require_special"`
	} `json:"password_policy"`

	TLS struct {
		Enabled  bool   `json:"enabled"`
		CertFile string `json:"cert_file"`
		KeyFile  string `json:"key_file"`
	} `json:"tls"`
}

// LoadSecurityConfig carrega as configurações de segurança
func LoadSecurityConfig() *SecurityConfig {
	config := &SecurityConfig{}

	// JWT
	config.JWT.Secret = "seu_jwt_secret_aqui_mudar_em_producao"
	config.JWT.ExpirationHrs = 24

	// Rate Limit
	config.RateLimit.Enabled = true
	config.RateLimit.RequestsMax = 100
	config.RateLimit.WindowSecs = 60

	// CORS
	config.CORS.AllowOrigins = []string{"*"}
	config.CORS.AllowCredentials = true
	config.CORS.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	config.CORS.AllowHeaders = []string{"Origin", "Content-Type", "Accept", "Authorization"}
	config.CORS.ExposeHeaders = []string{"Content-Length"}
	config.CORS.MaxAge = 86400 // 24 horas

	// Password Policy
	config.PasswordPolicy.MinLength = 8
	config.PasswordPolicy.RequireUpper = true
	config.PasswordPolicy.RequireLower = true
	config.PasswordPolicy.RequireNumber = true
	config.PasswordPolicy.RequireSpecial = true

	// TLS
	config.TLS.Enabled = false
	config.TLS.CertFile = "cert.pem"
	config.TLS.KeyFile = "key.pem"

	return config
}
