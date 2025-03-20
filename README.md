# Documentação Completa - Projeto PLC

## Sumário
1. [Visão Geral](#visão-geral)
2. [Estrutura do Projeto](#estrutura-do-projeto)
3. [Módulo de Configuração](#módulo-de-configuração)
4. [Módulo de Cache](#módulo-de-cache)
5. [Módulo de Sincronização](#módulo-de-sincronização)
6. [Módulo de Banco de Dados](#módulo-de-banco-de-dados)
7. [Módulo de Gerenciamento de PLCs](#módulo-de-gerenciamento-de-plcs)
8. [Diagramas de Arquitetura](#diagramas-de-arquitetura)
9. [Fluxos de Dados](#fluxos-de-dados)
10. [Considerações de Segurança](#considerações-de-segurança)
11. [Guia de Implementação](#guia-de-implementação)

## Visão Geral

O Projeto PLC é um sistema completo para monitoramento, comunicação e gerenciamento de Controladores Lógicos Programáveis (PLCs) industriais. O sistema é projetado com foco em resiliência, desempenho e confiabilidade, permitindo a coleta de dados em tempo real, configuração remota, e interação bidirecional com os equipamentos.

A arquitetura do sistema é modular, separando claramente as responsabilidades entre diferentes componentes:
- Configuração do sistema
- Armazenamento em cache
- Sincronização de dados
- Persistência em banco de dados
- Comunicação com PLCs

O projeto utiliza práticas modernas de desenvolvimento em Go, incluindo programação concorrente com goroutines, gerenciamento de ciclo de vida com contextos, e padrões de design como Repository, Adapter e Facade.

## Estrutura do Projeto

```
Projeto_PLC/
├── internal/
│   ├── config/          # Configurações do sistema
│   ├── cache/           # Sistema de cache (Redis e memória)
│   ├── configsync/      # Sincronização entre DB e cache
│   ├── database/        # Acesso a banco de dados e modelos
│   ├── plcmanager/      # Gerenciamento de conexões com PLCs
│   └── plc/             # Biblioteca de comunicação com PLCs
├── cmd/                 # Ponto de entrada da aplicação
└── api/                 # API para exposição dos serviços
```

## Módulo de Configuração

O módulo de configuração (`internal/config`) é responsável por definir, carregar e gerenciar as configurações globais do sistema.

### config.go

**Responsabilidade**: Definir a estrutura principal de configuração e métodos para carregar/salvar.

**Componentes principais**:
- `AppConfig`: Estrutura que contém todas as configurações do sistema
- `PLCManagerConfig`: Configurações específicas para o gerenciador de PLCs
- `DataCollectorConfig`: Configurações para coleta e armazenamento de dados
- `APIConfig`: Configurações para a API REST
- `DefaultConfig()`: Fornece valores padrão para todas as configurações
- `LoadConfig()`: Carrega configurações de um arquivo JSON ou cria um novo arquivo padrão
- `SaveConfig()`: Persiste configurações em arquivo JSON

**Funcionalidade**:
- Fornece valores padrão para todas as configurações do sistema
- Carrega configurações de arquivos externos em formato JSON
- Cria diretórios e arquivos de configuração se não existirem
- Persiste alterações em configurações

### database.go

**Responsabilidade**: Definir configurações específicas de bancos de dados.

**Componentes principais**:
- `DatabaseConfig`: Estrutura de configuração para diferentes bancos de dados
- `LoadDatabaseConfig()`: Carrega configurações padrão de banco de dados
- `GetPostgreSQLConnectionString()`: Formata string de conexão para PostgreSQL
- `GetPLCConfigConnectionString()`: Formata string de conexão para banco de configuração
- `GetTimescaleDBConnectionString()`: Formata string de conexão para TimescaleDB

**Funcionalidade**:
- Define parâmetros de conexão para diferentes bancos de dados
- Formata strings de conexão adequadas para cada banco
- Gerencia credenciais e parâmetros de conexão

### fiber_config.go

**Responsabilidade**: Configurar o framework web Fiber.

**Componentes principais**:
- `LoadFiberConfig()`: Cria e retorna a configuração para o servidor web Fiber

**Funcionalidade**:
- Define timeouts para leitura e escrita HTTP
- Configura limites de tamanho de corpo de requisição
- Define comportamento de cabeçalhos HTTP
- Configura comportamento de compressão e buffers

### security.go

**Responsabilidade**: Configurações de segurança da aplicação.

**Componentes principais**:
- `SecurityConfig`: Estrutura de configuração para aspectos de segurança
- `LoadSecurityConfig()`: Carrega configurações padrão de segurança

**Funcionalidade**:
- Configura JWT (JSON Web Tokens) para autenticação
- Define políticas de rate limiting para proteção contra ataques
- Configura CORS (Cross-Origin Resource Sharing)
- Define políticas de senha (complexidade, tamanho)
- Configura TLS/HTTPS

## Módulo de Cache

O módulo de cache (`internal/cache`) implementa um sistema flexível e resiliente para armazenamento em cache, com suporte a Redis e fallback para memória.

### cache.go

**Responsabilidade**: Definir a interface comum para todas as implementações de cache.

**Componentes principais**:
- Interface `Cache`: Define o contrato para todas as implementações de cache
- Métodos para manipulação de valores de tags (`SetTagValue`, `GetTagValue`, etc.)
- Métodos genéricos para manipulação de chaves/valores (`SetValue`, `GetValue`, etc.)

**Funcionalidade**:
- Define um contrato comum para diferentes implementações de cache
- Especifica métodos para operações específicas de PLCs
- Permite manipulação genérica de chaves/valores

### dynamic_cache.go

**Responsabilidade**: Implementar um sistema de cache dinâmico que alterna entre Redis e memória.

**Componentes principais**:
- `DynamicCache`: Estrutura que encapsula e gerencia diferentes implementações de cache
- `NewDynamicCache()`: Cria nova instância que tenta usar Redis e cai para memória se necessário
- `healthCheckRoutine()`: Goroutine que monitora a saúde do cache continuamente

**Funcionalidade**:
- Tenta usar Redis primariamente, mas cai para cache em memória em caso de falha
- Verifica periodicamente a saúde da conexão com Redis
- Tenta reconectar ao Redis quando estiver disponível novamente
- Delega operações para o cache ativo atual (Redis ou memória)

### memory_cache.go

**Responsabilidade**: Implementar cache em memória para uso local ou como fallback.

**Componentes principais**:
- `MemoryCache`: Estrutura que implementa cache em memória usando maps
- `NewMemoryCache()`: Cria nova instância de cache em memória
- Implementações de todos os métodos da interface `Cache`

**Funcionalidade**:
- Armazena dados em memória usando maps protegidos por mutex
- Suporta todas as operações da interface Cache
- Implementa conversões de tipos para operações numéricas
- Oferece suporte a operações com wildcards para listagem de chaves

### redis_cache.go

**Responsabilidade**: Implementar cache usando Redis como backend.

**Componentes principais**:
- `RedisCache`: Estrutura que encapsula cliente Redis
- `TagValue`: Estrutura para armazenar o valor, timestamp e qualidade de uma tag
- `NewRedisCache()`: Cria nova instância de cache Redis

**Funcionalidade**:
- Conecta e gerencia comunicação com o servidor Redis
- Serializa/deserializa valores complexos usando JSON
- Implementa todos os métodos da interface Cache
- Fornece operações de publicação para suportar Pub/Sub

### redis_cleaner.go

**Responsabilidade**: Limpar periodicamente chaves não essenciais no Redis.

**Componentes principais**:
- `RedisCleaner`: Estrutura para gerenciar limpeza de cache
- `Start()`: Inicia o processo de limpeza periódica
- `RunCleanup()`: Executa a limpeza de chaves específicas

**Funcionalidade**:
- Remove chaves antigas em intervalos configuráveis
- Processa chaves em lotes para evitar bloqueio do Redis
- Executa em background usando goroutines
- Fornece logging para monitoramento

## Módulo de Sincronização

O módulo de sincronização (`internal/configsync`) mantém os dados sincronizados entre o banco de dados PostgreSQL e o cache Redis.

### sync.go

**Responsabilidade**: Implementar sistema de sincronização principal com detecção inteligente de alterações.

**Componentes principais**:
- `ConfigSync`: Estrutura principal de sincronização
- `Start()`: Inicia a sincronização periódica
- `SyncAll()`: Sincroniza todos os dados de configuração
- `SyncPLCs()`: Sincroniza dados de PLCs
- `SyncTags()`: Sincroniza tags de PLCs

**Funcionalidade**:
- Sincroniza dados entre banco de dados e cache
- Verifica alterações usando hash MD5 para minimizar operações
- Implementa verificação de saúde do cache
- Usa mutex para evitar sincronizações simultâneas
- Mantém timestamps para rastrear última sincronização

### simplefix.go

**Responsabilidade**: Fornecer uma versão simplificada e robusta de sincronização para situações de emergência.

**Componentes principais**:
- `SimpleSyncManager`: Implementação simplificada de sincronização
- `SyncPLCs()`: Sincroniza PLCs usando SQL direto
- `SyncPLCTags()`: Sincroniza tags de PLC específico
- `SyncAll()`: Sincroniza todos PLCs e suas tags

**Funcionalidade**:
- Implementa sincronização direta usando SQL sem ORM
- Evita problemas de timeout e cancelamento de contexto
- Fornece abordagem mais robusta para sincronização de emergência
- Serializa e armazena dados no formato adequado para o cache

### notify_listener.go

**Responsabilidade**: Implementar sistema de notificação reativa para sincronização baseada em eventos.

**Componentes principais**:
- `PostgresListener`: Estrutura para escutar notificações do PostgreSQL
- `Start()`: Inicia processamento de notificações
- `processNotifications()`: Processa notificações recebidas

**Funcionalidade**:
- Usa sistema LISTEN/NOTIFY do PostgreSQL
- Reage a mudanças no banco de dados em tempo real
- Aciona sincronização específica conforme necessário
- Suporta reconexão automática ao banco de dados
- Processa eventos de alterações em PLCs e tags

## Módulo de Banco de Dados

O módulo de banco de dados (`internal/database`) implementa acesso a dados usando o padrão Repository.

### models.go

**Responsabilidade**: Definir estruturas de dados que representam entidades do sistema.

**Componentes principais**:
- `PLC`: Representa um controlador lógico programável
- `Tag`: Representa uma tag de dados do PLC
- `PLCStatus`: Status de conexão de um PLC
- `TagHistory`: Registro histórico de valor de tag
- `User`: Usuário do sistema
- `UserPermission`: Permissões de usuário para PLC
- `SystemLog`: Registro de log do sistema
- `NetworkConfig`: Configuração de rede
- `SystemConfig`: Configuração geral do sistema

**Funcionalidade**:
- Define as entidades principais do sistema
- Especifica os campos e tipos de dados para cada entidade
- Fornece tags para serialização JSON e mapeamento de banco de dados
- Define enumerações para valores com conjunto fixo de opções

### postgres.go

**Responsabilidade**: Implementar conexão e operações básicas com PostgreSQL.

**Componentes principais**:
- `DB`: Encapsula conexão e operações com PostgreSQL
- `NewDB()`: Cria nova conexão com configurações específicas
- Métodos para execução de queries com timeout
- Métodos para mapeamento entre resultados e structs

**Funcionalidade**:
- Gerencia conexão com PostgreSQL usando sqlx
- Implementa timeouts para evitar operações bloqueantes
- Fornece métodos seguros para execução de queries
- Mapeia resultados para structs Go
- Gerencia transações e pool de conexões

### config_repository.go

**Responsabilidade**: Implementar acesso a dados de configuração.

**Componentes principais**:
- Métodos para gerenciar configurações do sistema (`GetSystemConfig`, `SetSystemConfig`)
- Métodos para gerenciar configurações de rede (`GetNetworkConfig`, `CreateNetworkConfig`)

**Funcionalidade**:
- Fornece CRUD para configurações do sistema
- Gerencia configurações de rede com suporte a VLAN
- Implementa tratamento de erros específicos
- Validação de dados antes da persistência

### logger_repository.go

**Responsabilidade**: Implementar sistema de logging com persistência no banco de dados.

**Componentes principais**:
- `Logger`: Encapsula operações de logging
- Métodos para diferentes níveis de log (`Info`, `Warn`, `Error`, `Debug`)
- Métodos para consulta de logs por critérios

**Funcionalidade**:
- Persiste logs no banco de dados
- Suporta diferentes níveis de severidade
- Permite consultas por período, nível ou fonte
- Implementa limpeza automática de logs antigos

### plc_repository.go

**Responsabilidade**: Implementar acesso a dados de PLCs e tags.

**Componentes principais**:
- Métodos para gerenciar PLCs (`GetActivePLCs`, `GetPLCByID`, `CreatePLC`)
- Métodos para gerenciar tags (`GetPLCTags`, `GetTagByName`, `GetTagByID`)
- Métodos para gerenciar status de PLCs (`UpdatePLCStatus`)

**Funcionalidade**:
- Fornece CRUD para PLCs e suas tags
- Implementa consultas específicas como busca por rede
- Gerencia relacionamentos entre PLCs e tags
- Trata campos NULL e conversões de tipos
- Implementa transações para operações compostas

### user_repository.go

**Responsabilidade**: Implementar acesso a dados de usuários e autenticação.

**Componentes principais**:
- Métodos para gerenciar usuários (`GetUserByID`, `CreateUser`, `UpdateUser`)
- Métodos para gerenciar permissões (`GetUserPermissions`, `SetUserPermission`)
- Métodos para gerenciar sessões (`CreateUserSession`, `GetUserSession`)

**Funcionalidade**:
- Fornece CRUD para usuários do sistema
- Gerencia permissões de acesso a PLCs
- Implementa sistema de sessões com expiração
- Limpa sessões expiradas automaticamente
- Gerencia senhas com hash seguro

## Módulo de Gerenciamento de PLCs

O módulo de gerenciamento de PLCs (`internal/plcmanager`) é responsável por estabelecer e manter conexões com PLCs, monitorar valores e facilitar operações de leitura/escrita.

### manager.go

**Responsabilidade**: Implementar o gerenciador principal de PLCs.

**Componentes principais**:
- `Manager`: Estrutura principal que gerencia PLCs
- `Start()`: Inicia o gerenciamento de PLCs
- `Stop()`: Para o gerenciamento de PLCs
- `runPLCManager()`: Goroutine principal de gerenciamento

**Funcionalidade**:
- Detecta PLCs ativos automaticamente
- Gerencia conexões com múltiplos PLCs simultaneamente
- Inicia e encerra goroutines para cada PLC
- Implementa recuperação automática de falhas
- Usa contexts para gerenciamento de ciclo de vida

### status_monitor.go

**Responsabilidade**: Monitorar o status de conexão dos PLCs.

**Componentes principais**:
- `updatePLCStatus()`: Monitora periodicamente status de conexão
- `GetPLCStatus()`: Retorna status atual de um PLC
- `GetAllPLCStatus()`: Retorna status de todos os PLCs
- `ForcePingPLC()`: Força verificação de ping imediata

**Funcionalidade**:
- Realiza pings periódicos para verificar conectividade
- Atualiza status no banco de dados e cache
- Implementa contagem de falhas para evitar falsos positivos
- Fornece métodos para consulta de status
- Suporta diagnóstico com ping forçado

### tag_monitor.go

**Responsabilidade**: Monitorar e gerenciar tags de PLCs.

**Componentes principais**:
- `managePLCTags()`: Gerencia tags de um PLC
- `runTag()`: Executa coleta contínua de uma tag
- `GetTagValue()`: Obtém valor atual de uma tag
- `GetAllPLCTags()`: Retorna todos os valores de tags de um PLC
- `TestReadTag()`: Diagnóstico de leitura direta

**Funcionalidade**:
- Cria e gerencia goroutines para cada tag ativa
- Implementa taxas de scan configuráveis por tag
- Detecta e processa diferentes tipos de dados
- Publica alterações no cache
- Implementa otimizações para monitorar apenas mudanças
- Fornece ferramentas de diagnóstico

### writer.go

**Responsabilidade**: Implementar operações de escrita em PLCs.

**Componentes principais**:
- `WriteTagByName()`: Escreve valor em tag pelo nome
- `WriteTagByID()`: Escreve valor em tag pelo ID
- `WriteBulkTags()`: Escreve múltiplas tags de uma vez

**Funcionalidade**:
- Valida permissões de escrita antes de executar
- Verifica status online do PLC
- Estabelece conexões temporárias para escrita
- Suporta escrita em lote para múltiplas tags
- Atualiza cache após escrita bem-sucedida
- Implementa logging detalhado de operações

## Diagramas de Arquitetura

### Fluxo de Dados Principal

```
                  ┌────────────┐
                  │    API     │
                  └─────┬──────┘
                        │
                        ▼
┌─────────────┐    ┌────────────┐    ┌────────────┐
│  Database   │◄───┤ PLCManager ├───►│   Cache    │
└─────┬───────┘    └─────┬──────┘    └────┬───────┘
      │                  │                │
      │            ┌─────┴──────┐         │
      └───────────►│ ConfigSync ◄─────────┘
                   └────────────┘
```

### Arquitetura de Cache

```
┌───────────────┐
│ Dynamic Cache │
└───────┬───────┘
        │
        ├──────────────┐
        │              │
┌───────▼──────┐ ┌─────▼───────┐
│ Redis Cache  │ │ Memory Cache│
└──────────────┘ └─────────────┘
```

### Estrutura de Gerenciamento de PLCs

```
┌─────────────┐
│   Manager   │
└──────┬──────┘
       │
       ├─────────────┬──────────────┐
       │             │              │
┌──────▼─────┐ ┌─────▼────┐  ┌──────▼──────┐
│Status Monitor│ │Tag Monitor│  │  Writer    │
└──────────────┘ └──────────┘  └─────────────┘
```

## Fluxos de Dados

### Fluxo de Sincronização

1. ConfigSync verifica periodicamento o banco de dados para alterações
2. Calcula hash MD5 dos dados para detectar mudanças eficientemente
3. Se detectar alterações, serializa dados e atualiza o cache
4. Armazena hash e timestamp para verificações futuras
5. PostgresListener escuta eventos NOTIFY do PostgreSQL
6. Quando recebe notificação, aciona sincronização específica

### Fluxo de Monitoramento de Tags

1. PLCManager detecta PLCs ativos via cache ou banco de dados
2. Para cada PLC, inicia goroutine de gerenciamento
3. A goroutine do PLC verifica status e gerencia tags
4. Para cada tag ativa, inicia goroutine de monitoramento
5. A goroutine da tag realiza leituras periódicas no PLC
6. Valores lidos são armazenados no cache
7. Mudanças nos valores são publicadas para notificação

### Fluxo de Escrita em Tags

1. Aplicação solicita escrita em tag específica
2. PLCManager verifica permissões e status do PLC
3. Estabelece conexão temporária com o PLC
4. Realiza a operação de escrita no PLC
5. Atualiza o valor no cache
6. Publica notificação de alteração
7. Registra operação no log

## Considerações de Segurança

### Autenticação e Autorização

- Sistema de autenticação baseado em JWT
- Permissões granulares para acesso a PLCs
- Validação de tokens e verificação de expiração
- Sistema de sessões com controle de dispositivos

### Proteção de Dados

- Senhas armazenadas como hash (não em texto puro)
- Comunicação segura via TLS/HTTPS (quando ativado)
- Timeouts configuráveis para limitar exposição
- Rate limiting para proteção contra ataques

### Boas Práticas Implementadas

- Validação de todas as entradas do usuário
- Tratamento seguro de erros sem vazamento de informações
- Logging abrangente para auditoria
- Sanitização de dados antes de inserção no banco

## Guia de Implementação

### Requisitos do Sistema

- Go 1.16 ou superior
- PostgreSQL 12 ou superior
- TimescaleDB (extensão para PostgreSQL)
- Redis 6 ou superior

### Configuração Inicial

1. Clone o repositório
2. Configure o banco de dados PostgreSQL com as tabelas necessárias
3. Instale TimescaleDB como extensão para séries temporais
4. Configure um servidor Redis para cache
5. Ajuste as configurações no diretório `config`
6. Compile e execute o programa principal

### Considerações para Produção

1. Remova credenciais hardcoded e use variáveis de ambiente
2. Ative TLS para todas as comunicações externas
3. Configure backups do banco de dados
4. Implemente monitoramento para recursos críticos
5. Configure logs para rotação automática
6. Ajuste timeouts e configurações de pool conforme carga esperada

### Extensões Futuras

1. Implementação de API RESTful completa
2. Interface web para visualização e configuração
3. Sistema de alertas configuráveis
4. Integração com sistemas SCADA
5. Análise avançada de dados usando machine learning
6. Suporte a protocolos adicionais além de Siemens S7
