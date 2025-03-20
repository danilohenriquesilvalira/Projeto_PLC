package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"Projeto_PLC/config"
	"Projeto_PLC/internal/cache"
	"Projeto_PLC/internal/configsync"
	"Projeto_PLC/internal/database"
	"Projeto_PLC/internal/plcmanager"

	"github.com/lib/pq"
)

func main() {
	log.Println("Iniciando serviço de backend...")

	// Carrega configurações
	configPath := getEnv("CONFIG_FILE", "config/app.json")
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Printf("Aviso: Não foi possível carregar configurações do arquivo: %v. Usando configurações padrão.", err)
		cfg = config.DefaultConfig()
	}

	// Conectar ao PostgreSQL permanente (uso futuro)
	permanentDB, err := database.NewDB(cfg.Database.PostgreSQL)
	if err != nil {
		log.Printf("Aviso: Erro ao conectar ao banco de dados permanent_data: %v", err)
	} else {
		defer permanentDB.Close()
		log.Println("Conectado ao PostgreSQL permanent_data com sucesso")
	}

	// Conectar ao banco PLC_Config (para PLCs e Tags)
	plcDB, err := database.NewDB(cfg.Database.PLCConfig)
	if err != nil {
		log.Fatalf("Erro ao conectar ao banco de dados plc_config: %v", err)
	}
	defer plcDB.Close()
	log.Println("Conectado ao PostgreSQL plc_config com sucesso")

	// Criar logger
	logger := database.NewLogger(plcDB)
	logger.Info("Backend", "Sistema iniciado")

	// Criar o contexto principal para gerenciamento de goroutines
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Configurar o DynamicCache, que alterna entre Redis e cache em memória conforme a disponibilidade.
	redisCfg := cache.RedisConfig{
		Host:     cfg.Database.Redis.Host,
		Port:     cfg.Database.Redis.Port,
		Password: cfg.Database.Redis.Password,
	}
	cacheProvider := cache.NewDynamicCache(ctx, redisCfg)
	defer cacheProvider.Close()

	// Teste inicial do cache
	testCache(cacheProvider)

	// Configurar limpeza periódica simples para valores de runtime
	go func() {
		// Executar a cada hora
		cleanupTicker := time.NewTicker(1 * time.Hour)
		defer cleanupTicker.Stop()
		
		for {
			select {
			case <-ctx.Done():
				return
			case <-cleanupTicker.C:
				// Abordagem simples: listar e remover chaves conhecidas
				log.Println("Executando limpeza de chaves de runtime...")
				cleanRuntimeTags(cacheProvider)
			}
		}
	}()
	log.Println("Limpeza periódica de cache configurada")

	// Inicializar o sincronizador de configuração
	syncInterval := time.Duration(cfg.PLCManager.TagReloadInterval) * time.Second
	if syncInterval < 1*time.Second {
		syncInterval = 10 * time.Second
		log.Printf("Ajustando intervalo de sincronização para %v", syncInterval)
	}

	simpleSyncManager := configsync.NewSimpleSyncManager(plcDB, cacheProvider, logger)
	log.Println("Usando sincronizador simplificado para resolver problema de contexto...")
	if err := simpleSyncManager.SyncAll(); err != nil {
		log.Printf("Aviso: Erro na sincronização inicial: %v", err)
		log.Println("O sistema continuará, mas com desempenho reduzido")
	} else {
		log.Println("Sincronização inicial realizada com sucesso!")
	}

	// Configurar listener de notificações do PostgreSQL
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		cfg.Database.PLCConfig.Host, cfg.Database.PLCConfig.Port,
		cfg.Database.PLCConfig.User, cfg.Database.PLCConfig.Password,
		cfg.Database.PLCConfig.Database)

	pgListener := pq.NewListener(connStr, 10*time.Second, time.Minute, func(ev pq.ListenerEventType, err error) {
		if err != nil {
			log.Printf("Erro no listener PostgreSQL: %v", err)
		}
	})

	// Tentar conectar o listener
	if err := pgListener.Listen("plc_changes"); err != nil {
		log.Printf("Aviso: Não foi possível configurar listener para PLC changes: %v", err)
	} else if err := pgListener.Listen("tag_changes"); err != nil {
		log.Printf("Aviso: Não foi possível configurar listener para Tag changes: %v", err)
	} else if err := pgListener.Listen("plc_status_changes"); err != nil {
		log.Printf("Aviso: Não foi possível configurar listener para status changes: %v", err)
	} else {
		log.Println("Listener PostgreSQL configurado com sucesso")
		defer pgListener.Close()

		// Goroutine para processar notificações
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case notification := <-pgListener.Notify:
					if notification == nil {
						continue
					}

					log.Printf("Notificação recebida: %s - %s", notification.Channel, notification.Extra)

					// Processar baseado no canal
					switch notification.Channel {
					case "plc_changes", "plc_status_changes":
						// Sincronizar PLCs
						if _, err := simpleSyncManager.SyncPLCs(); err != nil {
							log.Printf("Erro ao sincronizar PLCs após notificação: %v", err)
						} else {
							log.Println("PLCs sincronizados após notificação")
						}

					case "tag_changes":
						// Sincronizar todas as tags
						if err := simpleSyncManager.SyncAll(); err != nil {
							log.Printf("Erro ao sincronizar tags após notificação: %v", err)
						} else {
							log.Println("Tags sincronizadas após notificação")
						}
					}
				}
			}
		}()
	}

	// Agende sincronizações periódicas:
	go func() {
		syncTicker := time.NewTicker(30 * time.Second)
		defer syncTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-syncTicker.C:
				if err := simpleSyncManager.SyncAll(); err != nil {
					log.Printf("Erro na sincronização periódica: %v", err)
				} else {
					log.Println("Sincronização periódica concluída com sucesso")
				}
			}
		}
	}()

	// Aguarda um momento para que a sincronização inicial ocorra
	log.Println("Aguardando sincronização inicial de configurações...")
	waitTime := 5 * time.Second
	log.Printf("Aguardando %v para a conclusão da sincronização inicial...", waitTime)
	time.Sleep(waitTime)

	// Verifica se a sincronização inicial funcionou
	verifySyncSuccess(cacheProvider)

	// Criar e iniciar o gerenciador de PLCs
	plcManager := plcmanager.NewManager(ctx, plcDB, cacheProvider, logger)
	if err := plcManager.Start(); err != nil {
		log.Printf("Erro ao iniciar gerenciador de PLCs: %v", err)
		log.Println("O sistema continuará em execução, mas a comunicação com PLCs pode estar limitada")
	} else {
		log.Println("Gerenciador de PLCs iniciado com sucesso")
	}

	// Configurar captura de sinais do sistema operacional para encerramento gracioso
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	// Aguardar sinal para encerrar
	sig := <-signalChan
	log.Printf("Recebido sinal %v, iniciando encerramento gracioso...", sig)
	cancel()

	log.Println("Aguardando encerramento de todos os componentes...")
	time.Sleep(2 * time.Second)

	logger.Info("Backend", "Sistema encerrado")
	log.Println("Serviço de backend encerrado com sucesso")
}

// cleanRuntimeTags remove tags de runtime do cache
func cleanRuntimeTags(cacheProvider cache.Cache) {
	// Abordagem sem ListKeys: limpar tags de PLCs conhecidos
	// Primeiro obter a lista de PLCs do cache
	plcsJSON, err := cacheProvider.GetValue("config:plcs:all")
	if err != nil || plcsJSON == "" {
		log.Printf("Não foi possível obter lista de PLCs para limpeza de cache: %v", err)
		return
	}

	// Tentativa básica de extração de IDs de PLC do JSON (sem depender de unmarshal)
	// Procura padrões como: "id":1
	plcIds := extractPLCIDs(plcsJSON)
	
	if len(plcIds) == 0 {
		log.Println("Nenhum PLC encontrado para limpeza de cache")
		return
	}

	removedCount := 0
	
	// Para cada PLC, obter suas tags e limpar valores de runtime
	for _, plcID := range plcIds {
		// Obter lista de tags para este PLC
		tagsJSON, err := cacheProvider.GetValue(fmt.Sprintf("config:plc:%d:tags", plcID))
		if err != nil || tagsJSON == "" {
			continue
		}
		
		// Extração simples de IDs de tags
		tagIds := extractTagIDs(tagsJSON)
		
		// Remover cada tag de runtime
		for _, tagID := range tagIds {
			runtimeKey := fmt.Sprintf("plc:%d:tag:%d", plcID, tagID)
			if err := cacheProvider.DeleteKey(runtimeKey); err == nil {
				removedCount++
			}
		}
	}
	
	log.Printf("Limpeza de cache: removidas %d chaves de runtime", removedCount)
}

// extractPLCIDs extrai IDs de PLC de um JSON (implementação simples)
func extractPLCIDs(plcsJSON string) []int {
	var plcIds []int
	// Simplificação: buscar "id":X onde X é um número
	parts := strings.Split(plcsJSON, "\"id\":")
	for i := 1; i < len(parts); i++ {
		var id int
		_, err := fmt.Sscanf(parts[i], "%d", &id)
		if err == nil && id > 0 {
			plcIds = append(plcIds, id)
		}
	}
	return plcIds
}

// extractTagIDs extrai IDs de tags de um JSON (implementação simples)
func extractTagIDs(tagsJSON string) []int {
	var tagIds []int
	// Simplificação: buscar "id":X onde X é um número
	parts := strings.Split(tagsJSON, "\"id\":")
	for i := 1; i < len(parts); i++ {
		var id int
		_, err := fmt.Sscanf(parts[i], "%d", &id)
		if err == nil && id > 0 {
			tagIds = append(tagIds, id)
		}
	}
	return tagIds
}

// testCache realiza um teste rápido do cache para verificar se está funcionando
func testCache(cacheProvider cache.Cache) {
	log.Println("Realizando teste de cache...")
	testKey := "system:test:" + time.Now().Format(time.RFC3339)
	testValue := "test-value-" + time.Now().String()

	// Teste de escrita
	if err := cacheProvider.SetValue(testKey, testValue); err != nil {
		log.Printf("ALERTA: Teste de escrita no cache falhou: %v", err)
	} else {
		log.Println("Teste de escrita no cache bem-sucedido")

		// Teste de leitura
		readValue, err := cacheProvider.GetValue(testKey)
		if err != nil {
			log.Printf("ALERTA: Teste de leitura no cache falhou: %v", err)
		} else if readValue != testValue {
			log.Printf("ALERTA: Verificação de dados do cache falhou. Esperado '%s', recebido '%s'",
				testValue, readValue)
		} else {
			log.Println("Teste de cache bem-sucedido: escrita e leitura funcionando corretamente")
		}

		// Limpar chave de teste
		_ = cacheProvider.DeleteKey(testKey)
	}
}

// verifySyncSuccess verifica se a sincronização inicial funcionou
func verifySyncSuccess(cacheProvider cache.Cache) {
	plcsJSON, err := cacheProvider.GetValue("config:plcs:all")
	if err != nil {
		log.Printf("ALERTA: Erro ao verificar sincronização de PLCs: %v", err)
	} else if plcsJSON == "" {
		log.Printf("ALERTA: Sincronização não armazenou dados de PLCs no cache!")
	} else {
		log.Println("Verificação básica de sincronização bem-sucedida: dados de PLCs encontrados no cache")
	}
}

// getEnv recupera uma variável de ambiente com fallback.
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}