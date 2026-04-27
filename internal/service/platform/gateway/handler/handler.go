package handler

import (
	"Logos/config"

	pbCollection "Logos/proto_gen/collection"
	pbCommon "Logos/proto_gen/common"
	pbExtraction "Logos/proto_gen/extraction"
	pbKnowledge "Logos/proto_gen/knowledge"
	pbMessage "Logos/proto_gen/message"
	pbMonitoring "Logos/proto_gen/monitoring"
	pbQuestion "Logos/proto_gen/question"
	pbRecommend "Logos/proto_gen/recommend"
	pbSearch "Logos/proto_gen/search"
	pbUser "Logos/proto_gen/user"
	pbVector "Logos/proto_gen/vector"
	"Logos/pkg/grpcserver"
	"Logos/pkg/obs"
)

type Handler struct {
	UserClient       pbUser.UserServiceClient
	KnowledgeClient  pbKnowledge.KnowledgeServiceClient
	SearchClient     pbSearch.SearchServiceClient
	VectorClient     pbVector.VectorServiceClient
	QuestionClient   pbQuestion.QAServiceClient
	RecommendClient  pbRecommend.RecommendationServiceClient
	ExtractionClient pbExtraction.KnowledgeExtractionServiceClient
	CollectionClient pbCollection.DataCollectionServiceClient
	MessageClient    pbMessage.MessageServiceClient
	MonitoringClient pbMonitoring.MonitoringServiceClient
}

func InitUserClient(cfg *config.Config) (pbUser.UserServiceClient, error) {
	_, _, clientOpt := obs.InitGRPCProvider("gateway-user-client")
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.user", clientOpt)
	if err != nil {
		return nil, err
	}
	return pbUser.NewUserServiceClient(conn), nil
}

func InitKnowledgeClient(cfg *config.Config) (pbKnowledge.KnowledgeServiceClient, error) {
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.knowledge")
	if err != nil {
		return nil, err
	}
	return pbKnowledge.NewKnowledgeServiceClient(conn), nil
}

func InitSearchClient(cfg *config.Config) (pbSearch.SearchServiceClient, error) {
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.search")
	if err != nil {
		return nil, err
	}
	return pbSearch.NewSearchServiceClient(conn), nil
}

func InitVectorClient(cfg *config.Config) (pbVector.VectorServiceClient, error) {
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.vector")
	if err != nil {
		return nil, err
	}
	return pbVector.NewVectorServiceClient(conn), nil
}

func InitQuestionClient(cfg *config.Config) (pbQuestion.QAServiceClient, error) {
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.question")
	if err != nil {
		return nil, err
	}
	return pbQuestion.NewQAServiceClient(conn), nil
}

func InitRecommendClient(cfg *config.Config) (pbRecommend.RecommendationServiceClient, error) {
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.recommend")
	if err != nil {
		return nil, err
	}
	return pbRecommend.NewRecommendationServiceClient(conn), nil
}

func InitExtractionClient(cfg *config.Config) (pbExtraction.KnowledgeExtractionServiceClient, error) {
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.extraction")
	if err != nil {
		return nil, err
	}
	return pbExtraction.NewKnowledgeExtractionServiceClient(conn), nil
}

func InitCollectionClient(cfg *config.Config) (pbCollection.DataCollectionServiceClient, error) {
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.collection")
	if err != nil {
		return nil, err
	}
	return pbCollection.NewDataCollectionServiceClient(conn), nil
}

func InitMessageClient(cfg *config.Config) (pbMessage.MessageServiceClient, error) {
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.message")
	if err != nil {
		return nil, err
	}
	return pbMessage.NewMessageServiceClient(conn), nil
}

func InitMonitoringClient(cfg *config.Config) (pbMonitoring.MonitoringServiceClient, error) {
	conn, err := grpcserver.NewGRPCClientConn(cfg.Etcd.Endpoints, "logos.monitoring")
	if err != nil {
		return nil, err
	}
	return pbMonitoring.NewMonitoringServiceClient(conn), nil
}

func getBaseRespMessage(resp *pbCommon.BaseResp) string {
	if resp == nil {
		return "success"
	}
	return resp.StatusMessage
}
