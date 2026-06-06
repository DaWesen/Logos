package main

import (
	"context"
	"log"
	"time"

	"Logos/config"
	"Logos/internal/bot"
	botMemory "Logos/internal/bot/memory"
	botTools "Logos/internal/bot/tools"
	"Logos/internal/service/ai/bot/dao"
	"Logos/internal/service/ai/bot/handler"
	"Logos/internal/service/ai/bot/model"
	"Logos/internal/service/ai/bot/service"
	"Logos/pkg/client"
	"Logos/pkg/database/pgsql"
	"Logos/pkg/eino"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
	"Logos/pkg/obs"
	"Logos/pkg/outbox"
	pb "Logos/proto_gen/bot"

	"google.golang.org/grpc"
)

type vectorServiceAdapter struct {
	client *client.VectorClient
}

func (a *vectorServiceAdapter) TextSearch(ctx context.Context, collectionID string, text string, topK int) ([]string, error) {
	return a.client.TextSearch(ctx, collectionID, text, topK)
}

func (a *vectorServiceAdapter) SearchWithScores(ctx context.Context, collectionID string, text string, topK int) ([]*service.VectorSearchResult, error) {
	results, err := a.client.SearchWithScores(ctx, collectionID, text, topK)
	if err != nil {
		return nil, err
	}
	out := make([]*service.VectorSearchResult, len(results))
	for i, r := range results {
		out[i] = &service.VectorSearchResult{
			ID:       r.ID,
			Content:  r.Content,
			Score:    r.Score,
			Metadata: r.Metadata,
		}
	}
	return out, nil
}

func (a *vectorServiceAdapter) ListCollections(ctx context.Context) ([]*service.VectorCollectionInfo, error) {
	collections, err := a.client.ListCollections(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*service.VectorCollectionInfo, len(collections))
	for i, c := range collections {
		out[i] = &service.VectorCollectionInfo{
			ID:   c.ID,
			Name: c.Name,
			Size: c.Size,
		}
	}
	return out, nil
}

func (a *vectorServiceAdapter) UpdateCollectionEmbedding(ctx context.Context, collectionID, model, baseURL, apiKey string) error {
	parameters := map[string]string{
		"__embedding_model":    model,
		"__embedding_base_url": baseURL,
		"__embedding_api_key":  apiKey,
	}
	return a.client.UpdateCollection(ctx, collectionID, "", parameters)
}

func (a *vectorServiceAdapter) Vectorize(ctx context.Context, text string, collectionID string, metadata map[string]string) (string, error) {
	result, err := a.client.Vectorize(ctx, text, collectionID, metadata)
	if err != nil {
		return "", err
	}
	return result.GetID(), nil
}

type searchServiceAdapter struct {
	client *client.SearchClient
}

func (a *searchServiceAdapter) Search(ctx context.Context, query string, topK int) ([]*service.SearchResultItem, error) {
	results, err := a.client.SearchWithResults(ctx, query, topK)
	if err != nil {
		return nil, err
	}
	out := make([]*service.SearchResultItem, len(results))
	for i, r := range results {
		out[i] = &service.SearchResultItem{
			ID:      r.ID,
			Title:   r.Title,
			Content: r.Content,
			Score:   r.Score,
		}
	}
	return out, nil
}

type graphWriteAdapter struct {
	*client.KnowledgeClient
}

func (a *graphWriteAdapter) FindOrCreateEntity(ctx context.Context, entityType, name, collectionID string, properties map[string]string, description *string, color string) (*botTools.GraphEntity, error) {
	entity, err := a.KnowledgeClient.FindOrCreateEntity(ctx, entityType, name, collectionID, properties, description, color)
	if err != nil {
		return nil, err
	}
	return convertGraphEntity(entity), nil
}

func (a *graphWriteAdapter) AddRelation(ctx context.Context, relationType, sourceID, targetID, collectionID string, properties map[string]string, description *string) (*botTools.GraphRelation, error) {
	relation, err := a.KnowledgeClient.AddRelationWithCollection(ctx, relationType, sourceID, targetID, collectionID, properties, description)
	if err != nil {
		return nil, err
	}
	return convertGraphRelation(relation), nil
}

func (a *graphWriteAdapter) UpdateEntity(ctx context.Context, id, entityType, name, collectionID string, properties map[string]string, description *string, color string) (*botTools.GraphEntity, error) {
	entity, err := a.KnowledgeClient.UpdateEntityDetails(ctx, id, entityType, name, properties, description, collectionID)
	if err != nil {
		return nil, err
	}
	return convertGraphEntity(entity), nil
}

func (a *graphWriteAdapter) DeleteEntity(ctx context.Context, id string) error {
	return a.KnowledgeClient.DeleteEntity(ctx, id)
}

func (a *graphWriteAdapter) DeleteRelation(ctx context.Context, id string) error {
	return a.KnowledgeClient.DeleteRelation(ctx, id)
}

type graphSearchAdapter struct {
	*client.KnowledgeClient
}

func (a *graphSearchAdapter) SearchEntities(ctx context.Context, keyword string, entityType *string, collectionID string, page, pageSize int) ([]*botTools.GraphEntity, error) {
	entities, err := a.KnowledgeClient.SearchEntitiesWithDetails(ctx, keyword, entityType, collectionID, page, pageSize)
	if err != nil {
		return nil, err
	}
	out := make([]*botTools.GraphEntity, len(entities))
	for i, e := range entities {
		out[i] = convertGraphEntity(e)
	}
	return out, nil
}

func (a *graphSearchAdapter) GetRelatedEntities(ctx context.Context, entityID string, relationType string) ([]*botTools.GraphEntity, error) {
	entities, err := a.KnowledgeClient.GetRelatedEntitiesDetails(ctx, entityID, relationType)
	if err != nil {
		return nil, err
	}
	out := make([]*botTools.GraphEntity, len(entities))
	for i, e := range entities {
		out[i] = convertGraphEntity(e)
	}
	return out, nil
}

func (a *graphSearchAdapter) GetSubgraph(ctx context.Context, entityID string, depth int, collectionID string) (*botTools.GraphSubgraph, error) {
	sg, err := a.KnowledgeClient.GetSubgraphDetails(ctx, entityID, depth, collectionID)
	if err != nil {
		return nil, err
	}
	nodes := make([]*botTools.GraphEntity, len(sg.Nodes))
	for i, n := range sg.Nodes {
		nodes[i] = convertGraphEntity(n)
	}
	edges := make([]*botTools.GraphRelation, len(sg.Edges))
	for i, e := range sg.Edges {
		edges[i] = convertGraphRelation(e)
	}
	return &botTools.GraphSubgraph{
		Nodes:     nodes,
		Edges:     edges,
		NodeCount: sg.NodeCount,
		EdgeCount: sg.EdgeCount,
	}, nil
}

func (a *graphSearchAdapter) GetEntityPaths(ctx context.Context, sourceID, targetID string, maxDepth int, collectionID string) ([]*botTools.GraphEntityPath, error) {
	paths, err := a.KnowledgeClient.GetEntityPathsDetails(ctx, sourceID, targetID, maxDepth, collectionID)
	if err != nil {
		return nil, err
	}
	out := make([]*botTools.GraphEntityPath, len(paths))
	for i, p := range paths {
		entities := make([]*botTools.GraphEntity, len(p.Entities))
		for j, e := range p.Entities {
			entities[j] = convertGraphEntity(e)
		}
		edges := make([]*botTools.GraphRelation, len(p.Edges))
		for j, e := range p.Edges {
			edges[j] = convertGraphRelation(e)
		}
		out[i] = &botTools.GraphEntityPath{
			Entities: entities,
			Edges:    edges,
			Length:   p.Length,
		}
	}
	return out, nil
}

func (a *graphSearchAdapter) GetGraphStats(ctx context.Context, collectionID string) (*botTools.GraphStatsInfo, error) {
	stats, err := a.KnowledgeClient.GetGraphStatsDetails(ctx, collectionID)
	if err != nil {
		return nil, err
	}
	return &botTools.GraphStatsInfo{
		EntityCount:       stats.EntityCount,
		RelationCount:     stats.RelationCount,
		EntityTypeCount:   stats.EntityTypeCount,
		RelationTypeCount: stats.RelationTypeCount,
	}, nil
}

func convertGraphEntity(e *client.GraphEntity) *botTools.GraphEntity {
	if e == nil {
		return nil
	}
	return &botTools.GraphEntity{
		ID:           e.ID,
		Type:         e.Type,
		Name:         e.Name,
		Properties:   e.Properties,
		Description:  e.Description,
		CollectionID: e.CollectionID,
		Color:        e.Color,
	}
}

func convertGraphRelation(r *client.GraphRelation) *botTools.GraphRelation {
	if r == nil {
		return nil
	}
	return &botTools.GraphRelation{
		ID:          r.ID,
		Type:        r.Type,
		SourceID:    r.SourceID,
		TargetID:    r.TargetID,
		Properties:  r.Properties,
		Description: r.Description,
	}
}

func main() {
	cfg := config.GetConfig()

	logger.InitLogger()

	db, err := pgsql.InitPostgres()
	if err != nil {
		log.Fatalf("Failed to init postgres: %v", err)
	}

	if err := model.AutoMigrate(db); err != nil {
		log.Fatalf("Failed to auto migrate: %v", err)
	}

	if err := outbox.AutoMigrate(db); err != nil {
		log.Fatalf("Failed to auto migrate outbox: %v", err)
	}

	if err := bot.Init(); err != nil {
		log.Printf("Failed to init bot module: %v", err)
	}

	agentManager := bot.GetAgentManager()
	einoManager := eino.GetEinoManager()

	var billingService service.BillingService
	billingClient, err := client.NewBillingClientFromConfig(cfg)
	if err != nil {
		log.Printf("⚠️ Failed to init billing client: %v (billing will be disabled)", err)
	} else {
		billingService = billingClient
		log.Printf("✅ Billing client initialized successfully")
	}

	var vectorService service.VectorService
	vectorClient, err := client.TryDialVectorWithFallback(cfg)
	if err != nil {
		log.Printf("Failed to init vector client: %v", err)
	} else {
		vectorService = &vectorServiceAdapter{client: vectorClient}
	}

	var searchService service.SearchService
	searchClient, err := client.TryDialSearchWithFallback(cfg)
	if err != nil {
		log.Printf("Failed to init search client: %v", err)
	} else {
		searchService = &searchServiceAdapter{client: searchClient}
	}

	var producer *mq.Producer
	if len(cfg.Kafka.Brokers) > 0 {
		producer = mq.NewProducer()
	}

	var mcpClient *client.MCPClient
	mcpCli, err := client.TryDialMCPWithFallback(cfg)
	if err != nil {
		log.Printf("Failed to init MCP client: %v", err)
	} else {
		mcpClient = mcpCli
	}

	var knowledgeService botTools.GraphWriteService
	var graphSearchSvc botTools.GraphService
	knowledgeClient, err := client.TryDialKnowledgeWithFallback(cfg)
	if err != nil {
		log.Printf("Failed to init knowledge client: %v", err)
	} else {
		knowledgeService = &graphWriteAdapter{KnowledgeClient: knowledgeClient}
		graphSearchSvc = &graphSearchAdapter{KnowledgeClient: knowledgeClient}
	}

	repo := dao.NewBotRepository(db)
	outboxRepo := outbox.NewOutboxRepository()
	botService := service.NewBotService(repo, agentManager, einoManager, billingService, vectorService, searchService, outboxRepo, mcpClient, cfg, knowledgeService, graphSearchSvc)

	if producer != nil {
		relay := outbox.NewRelay(db, producer)
		relay.Start()
		defer relay.Stop()
	}

	memoryMgr := botMemory.GetMemoryManager(repo, einoManager, agentManager)
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = memoryMgr.CleanupOldMemories(ctx, "", "", 30*24*time.Hour)
			cancel()
		}
	}()

	shutdown, serverOpt, _ := obs.InitGRPCProvider("bot")
	defer shutdown(context.Background())

	if err := grpcserver.StartServer(grpcserver.ServerConfig{
		ServiceName: "logos.bot",
		Port:        cfg.Ports.Bot,
		Etcd:        grpcserver.EtcdConfig{Endpoints: cfg.Etcd.Endpoints},
	}, func(s *grpc.Server) {
		pb.RegisterBotServiceServer(s, &handler.BotServiceImpl{
			BotService: botService,
		})
	}, serverOpt); err != nil {
		log.Fatalf("Bot service failed to run: %v", err)
	}
}
