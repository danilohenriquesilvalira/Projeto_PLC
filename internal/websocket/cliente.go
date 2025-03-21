package websocket

import (
	"encoding/json"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

// Cliente representa uma conexão WebSocket com um cliente
type Cliente struct {
	gerenciador *Gerenciador
	conn        *websocket.Conn
	enviar      chan MensagemWS
	filtroPlcID int // 0 significa sem filtro
}

// bombearEscrita envia mensagens para o WebSocket
func (c *Cliente) bombearEscrita() {
	defer func() {
		c.conn.Close()
	}()

	// Configura um ping periódico para manter a conexão
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case mensagem, ok := <-c.enviar:
			// Configura o tempo limite de escrita
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				// Canal fechado, encerra conexão
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			// Envia a mensagem como JSON
			if err := c.conn.WriteJSON(mensagem); err != nil {
				log.Printf("Erro ao enviar mensagem: %v", err)
				return
			}

		case <-ticker.C:
			// Envia ping periódico
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// bombearLeitura lê mensagens do WebSocket
func (c *Cliente) bombearLeitura() {
	defer func() {
		c.gerenciador.desregistrar <- c
		c.conn.Close()
	}()

	// Configura parâmetros do WebSocket
	c.conn.SetReadLimit(4096) // 4KB
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseAbnormalClosure) {
				log.Printf("Erro na leitura do WebSocket: %v", err)
			}
			break
		}

		// Processa comandos recebidos
		var cmd ComandoEscrita
		if err := json.Unmarshal(message, &cmd); err != nil {
			log.Printf("Erro ao decodificar comando: %v", err)
			continue
		}

		if cmd.Tag != "" {
			log.Printf("Comando de escrita recebido: Tag=%s, Value=%v", cmd.Tag, cmd.Value)
			c.gerenciador.processarEscritaTag(cmd.Tag, cmd.Value)
		}
	}
}
