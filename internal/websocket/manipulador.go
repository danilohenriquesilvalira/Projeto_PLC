package websocket

import (
	"log"
	"net/http"
	"strconv"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Permitir conexões de qualquer origem (em produção, restrinja isso)
	},
}

// ManipularWS manipula as requisições WebSocket
func (g *Gerenciador) ManipularWS(w http.ResponseWriter, r *http.Request) {
	// Extrai o filtro de PLC da query string, se existir
	plcIDStr := r.URL.Query().Get("plc_id")
	var filtroPlcID int
	if plcIDStr != "" {
		var err error
		filtroPlcID, err = strconv.Atoi(plcIDStr)
		if err != nil {
			http.Error(w, "plc_id inválido", http.StatusBadRequest)
			return
		}
	}

	// Faz o upgrade da conexão HTTP para WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Erro ao abrir conexão WebSocket: %v", err)
		http.Error(w, "Erro ao abrir conexão WebSocket", http.StatusBadRequest)
		return
	}

	// Cria um novo cliente
	cliente := &Cliente{
		conn:        conn,
		gerenciador: g,
		enviar:      make(chan MensagemWS, 256),
		filtroPlcID: filtroPlcID,
	}

	// Registra o cliente no gerenciador
	g.registrar <- cliente

	// Inicia as goroutines para leitura e escrita
	go cliente.bombearEscrita()
	go cliente.bombearLeitura()

	log.Printf("Nova conexão WebSocket aceita")
}
