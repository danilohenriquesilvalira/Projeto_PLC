package config

import (
	"time"

	"github.com/gofiber/fiber/v2"
)

// LoadFiberConfig retorna a configuração para o Fiber API
func LoadFiberConfig() fiber.Config {
	config := fiber.Config{
		Prefork:              false,
		ServerHeader:         "PLC Control Server",
		StrictRouting:        true,
		CaseSensitive:        true,
		Immutable:            false,
		UnescapePath:         true,
		ETag:                 true,
		BodyLimit:            4 * 1024 * 1024, // 4MB
		ReadTimeout:          30 * time.Second,
		WriteTimeout:         30 * time.Second,
		IdleTimeout:          120 * time.Second,
		ReadBufferSize:       4096,
		WriteBufferSize:      4096,
		CompressedFileSuffix: ".gz",
		ProxyHeader:          "X-Forwarded-For",
		GETOnly:              false,
		DisableKeepalive:     false,
		DisableDefaultDate:   false,
		// DisableDefaultContent: false, // Removido - não suportado na sua versão do Fiber
		DisableHeaderNormalizing: false,
		DisableStartupMessage:    false,
		ReduceMemoryUsage:        false,
	}

	return config
}
