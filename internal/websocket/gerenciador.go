package websocket

import (
	"log"
	"sync"
	"time"

	"Projeto_PLC/internal/cache"
	"Projeto_PLC/internal/database"
	"Projeto_PLC/internal/plcmanager" // Adicionado para integração com escrita em PLCs
)

// Gerenciador gerencia todas as conexões WebSocket e distribuição de dados
type Gerenciador struct {
	clientes     map[*Cliente]bool
	broadcast    chan MensagemWS
	registrar    chan *Cliente
	desregistrar chan *Cliente
	cache        cache.Cache
	db           *database.DB
	plcManager   *plcmanager.Manager // Novo campo para acessar o plcmanager
	mutex        sync.RWMutex
	wg           sync.WaitGroup // WaitGroup para encerramento gracioso

	// PLCs monitorados
	plcsMonitorados map[int]bool
	plcsMutex       sync.RWMutex

	// Canal para controlar o encerramento
	doneChan chan struct{}
}

// NovoGerenciador cria uma nova instância do gerenciador WebSocket
func NovoGerenciador(db *database.DB, cacheProvider cache.Cache, plcManager *plcmanager.Manager) *Gerenciador {
	return &Gerenciador{
		clientes:        make(map[*Cliente]bool),
		broadcast:       make(chan MensagemWS, 100), // Buffer aumentado para evitar bloqueio
		registrar:       make(chan *Cliente),
		desregistrar:    make(chan *Cliente),
		db:              db,
		cache:           cacheProvider,
		plcManager:      plcManager, // Inicializa o campo plcManager
		plcsMonitorados: make(map[int]bool),
		doneChan:        make(chan struct{}),
	}
}

// Iniciar inicia o loop do gerenciador WebSocket
func (g *Gerenciador) Iniciar() {
	g.wg.Add(2) // Adiciona 2 goroutines para WaitGroup (coletarDados e o loop principal)

	go g.coletarDados()

	go func() {
		defer g.wg.Done()

		for {
			select {
			case <-g.doneChan:
				log.Println("Encerrando loop do gerenciador WebSocket")
				return

			case cliente := <-g.registrar:
				g.mutex.Lock()
				g.clientes[cliente] = true
				log.Printf("Novo cliente WebSocket conectado. Total: %d", len(g.clientes))
				g.mutex.Unlock()

			case cliente := <-g.desregistrar:
				g.mutex.Lock()
				if _, ok := g.clientes[cliente]; ok {
					delete(g.clientes, cliente)
					close(cliente.enviar)
					log.Printf("Cliente WebSocket desconectado. Total: %d", len(g.clientes))
				}
				g.mutex.Unlock()

			case mensagem := <-g.broadcast:
				g.mutex.Lock()
				for cliente := range g.clientes {
					// Filtra mensagens por PLC se o cliente tiver um filtro
					if cliente.filtroPlcID == 0 || cliente.filtroPlcID == mensagem.PLC.ID {
						select {
						case cliente.enviar <- mensagem:
						default:
							close(cliente.enviar)
							delete(g.clientes, cliente)
						}
					}
				}
				g.mutex.Unlock()
			}
		}
	}()
}

// Parar encerra o gerenciador de WebSocket de forma graciosa
func (g *Gerenciador) Parar() {
	log.Println("Iniciando encerramento do gerenciador WebSocket...")

	// Sinaliza para as goroutines que devem encerrar
	close(g.doneChan)

	// Fecha todas as conexões de cliente
	g.mutex.Lock()
	for cliente := range g.clientes {
		close(cliente.enviar)
	}
	g.clientes = make(map[*Cliente]bool)
	g.mutex.Unlock()

	// Aguarda todas as goroutines terminarem (com timeout)
	waitChan := make(chan struct{})
	go func() {
		g.wg.Wait()
		close(waitChan)
	}()

	select {
	case <-waitChan:
		log.Println("Gerenciador WebSocket encerrado com sucesso")
	case <-time.After(5 * time.Second):
		log.Println("Timeout ao aguardar encerramento de goroutines do WebSocket")
	}
}

// AdicionarPLCMonitorado adiciona um PLC à lista de PLCs monitorados
func (g *Gerenciador) AdicionarPLCMonitorado(plcID int) {
	g.plcsMutex.Lock()
	defer g.plcsMutex.Unlock()

	g.plcsMonitorados[plcID] = true
	log.Printf("PLC ID %d adicionado para monitoramento via WebSocket", plcID)
}

// coletarDados coleta dados dos PLCs a cada intervalo
func (g *Gerenciador) coletarDados() {
	defer g.wg.Done()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-g.doneChan:
			log.Println("Encerrando coleta de dados do WebSocket")
			return

		case <-ticker.C:
			g.plcsMutex.RLock()
			plcIDs := make([]int, 0, len(g.plcsMonitorados))
			for plcID := range g.plcsMonitorados {
				plcIDs = append(plcIDs, plcID)
			}
			g.plcsMutex.RUnlock()

			if len(plcIDs) == 0 {
				continue
			}

			for _, plcID := range plcIDs {
				// Obter detalhes do PLC do banco de dados
				plcInfo, err := g.db.GetPLCByID(plcID)
				if err != nil {
					log.Printf("Erro ao buscar PLC ID %d: %v", plcID, err)
					continue
				}

				// Obter valores das tags do PLC do cache
				tagValues, err := g.cache.GetAllPLCTags(plcID)
				if err != nil {
					log.Printf("Erro ao obter tags do PLC %d: %v", plcID, err)
					continue
				}

				// Prepara a mensagem
				msg := MensagemWS{
					PLC: StatusPLC{
						ID:                plcInfo.ID,
						Name:              plcInfo.Name,
						Status:            plcInfo.Status,
						UltimaAtualizacao: time.Now().Format(time.RFC3339), // Usando hora atual como fallback
					},
					Tags: make([]ValorTag, 0, len(tagValues)),
				}

				// Obter lista de tags deste PLC
				tags, err := g.db.GetPLCTags(plcID)
				if err != nil {
					log.Printf("Erro ao buscar tags do PLC %d: %v", plcID, err)
					continue
				}

				// Mapa para armazenar informações das tags
				tagInfoMap := make(map[int]database.Tag)
				for _, tag := range tags {
					tagInfoMap[tag.ID] = tag
				}

				// Adiciona os valores das tags
				for tagID, tagValue := range tagValues {
					tagInfo, exists := tagInfoMap[tagID]
					if !exists {
						continue
					}

					msg.Tags = append(msg.Tags, ValorTag{
						ID:        tagID,
						Nome:      tagInfo.Name,
						Valor:     tagValue.Value,
						Qualidade: tagValue.Quality,
					})
				}

				// Envia a mensagem se houver tags
				if len(msg.Tags) > 0 {
					select {
					case g.broadcast <- msg:
						// Mensagem enviada com sucesso
					case <-g.doneChan:
						return
					default:
						log.Printf("Canal de broadcast cheio, pulando mensagem para PLC %d", plcID)
					}
				}
			}
		}
	}
}

// processarEscritaTag processa um comando de escrita para uma tag
func (g *Gerenciador) processarEscritaTag(tagName string, value interface{}) {
	log.Printf("Processando comando de escrita: Tag=%s, Value=%v", tagName, value)

	// Verifica se o gerenciador plcManager está disponível
	if g.plcManager == nil {
		log.Printf("Erro: PLCManager não disponível para escrita na tag %s", tagName)
		return
	}

	// Usa o plcManager para escrever na tag física
	err := g.plcManager.WriteTagByName(tagName, value)

	if err != nil {
		log.Printf("Erro ao escrever na tag %s: %v", tagName, err)
		return
	}

	log.Printf("Valor escrito com sucesso na tag %s: %v", tagName, value)
}
