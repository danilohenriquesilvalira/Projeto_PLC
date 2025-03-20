package cache

// Cache define a interface para operações de cache
type Cache interface {
	// Close fecha a conexão com o cache
	Close() error

	// SetTagValue armazena o valor de uma tag
	SetTagValue(plcID, tagID int, value interface{}) error

	// GetTagValue recupera o valor de uma tag
	GetTagValue(plcID, tagID int) (*TagValue, error)

	// GetTagValueAsNumber retorna o valor numérico da tag
	GetTagValueAsNumber(plcID, tagID int) (float64, error)

	// GetRawValue recupera um valor bruto pela chave
	GetRawValue(key string) (interface{}, error)

	// SetTagValueWithQuality armazena o valor com qualidade personalizada
	SetTagValueWithQuality(plcID, tagID int, value interface{}, quality int) error

	// RegisterTagName registra o mapeamento entre nome da tag e seu ID
	RegisterTagName(plcID, tagID int, tagName string) error

	// GetTagValueByName recupera o valor de uma tag pelo nome
	GetTagValueByName(plcID int, tagName string) (*TagValue, error)

	// PublishTagUpdate publica uma atualização de tag
	PublishTagUpdate(plcID, tagID int, value interface{}) error

	// GetAllPLCTags retorna todos os valores de tags de um PLC
	GetAllPLCTags(plcID int) (map[int]*TagValue, error)

	// Novos métodos para o ConfigSync

	// SetValue armazena uma string arbitrária no cache
	SetValue(key string, value string) error

	// GetValue recupera uma string arbitrária do cache
	GetValue(key string) (string, error)

	// DeleteKey remove uma chave do cache
	DeleteKey(key string) error

	// ListKeys lista todas as chaves que correspondem a um padrão
	ListKeys(pattern string) ([]string, error)
}
