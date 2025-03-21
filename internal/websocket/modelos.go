package websocket

// MensagemWS representa a mensagem completa enviada pelo WebSocket para atualizações de status
type MensagemWS struct {
	PLC  StatusPLC  `json:"plc"`
	Tags []ValorTag `json:"tags"`
}

// StatusPLC representa o status do PLC
type StatusPLC struct {
	ID                int    `json:"plc_id"`
	Name              string `json:"name"`
	Status            string `json:"status"`
	UltimaAtualizacao string `json:"last_update"`
}

// ValorTag representa o valor de uma tag
type ValorTag struct {
	ID        int         `json:"id"`
	Nome      string      `json:"name"`
	Valor     interface{} `json:"value"`
	Qualidade int         `json:"quality"`
}

// ComandoEscrita representa o comando de escrita enviado pelo front-end via WebSocket
type ComandoEscrita struct {
	Tag   string      `json:"tag"`
	Value interface{} `json:"value"`
}
