package graph

import (
	"context"
	"fmt"
	"sync"

	"Noah/config"
	"Noah/pkg/logger"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

var (
	neo4jDriver neo4j.DriverWithContext
	neo4jOnce   sync.Once
)

type Neo4jManager struct {
	driver neo4j.DriverWithContext
}

func InitNeo4j() (neo4j.DriverWithContext, error) {
	var err error
	neo4jOnce.Do(func() {
		cfg := config.GetConfig()
		neo4jConfig := cfg.Neo4j

		neo4jDriver, err = neo4j.NewDriverWithContext(
			neo4jConfig.URI,
			neo4j.BasicAuth(neo4jConfig.User, neo4jConfig.Password, ""),
		)
		if err != nil {
			err = fmt.Errorf("failed to create neo4j driver: %w", err)
			logger.Error("创建Neo4j驱动失败", logger.ErrorField(err))
			return
		}

		err = neo4jDriver.VerifyConnectivity(context.Background())
		if err != nil {
			err = fmt.Errorf("failed to verify neo4j connectivity: %w", err)
			logger.Error("Neo4j连接验证失败", logger.ErrorField(err))
			return
		}

		logger.Info("Neo4j初始化成功")
	})

	return neo4jDriver, err
}

func NewNeo4jManager(driver neo4j.DriverWithContext) *Neo4jManager {
	return &Neo4jManager{
		driver: driver,
	}
}

func (n *Neo4jManager) CreateNode(ctx context.Context, label string, id string, properties map[string]interface{}) error {
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	params := map[string]interface{}{
		"id":         id,
		"properties": properties,
	}

	query := fmt.Sprintf(`
		CREATE (n:%s {id: $id})
		SET n += $properties
		RETURN n
	`, label)

	_, err := session.Run(ctx, query, params)
	if err != nil {
		logger.Error("创建节点失败",
			logger.StringField("label", label),
			logger.StringField("id", id),
			logger.ErrorField(err))
		return err
	}

	logger.Info("创建节点成功",
		logger.StringField("label", label),
		logger.StringField("id", id))
	return nil
}

func (n *Neo4jManager) UpdateNode(ctx context.Context, label string, id string, properties map[string]interface{}) error {
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	params := map[string]interface{}{
		"id":         id,
		"properties": properties,
	}

	query := fmt.Sprintf(`
		MATCH (n:%s {id: $id})
		SET n += $properties
		RETURN n
	`, label)

	_, err := session.Run(ctx, query, params)
	if err != nil {
		logger.Error("更新节点失败",
			logger.StringField("label", label),
			logger.StringField("id", id),
			logger.ErrorField(err))
		return err
	}

	logger.Info("更新节点成功",
		logger.StringField("label", label),
		logger.StringField("id", id))
	return nil
}

func (n *Neo4jManager) DeleteNode(ctx context.Context, label string, id string) error {
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	params := map[string]any{
		"id": id,
	}

	query := fmt.Sprintf(`
		MATCH (n:%s {id: $id})
		DETACH DELETE n
	`, label)

	_, err := session.Run(ctx, query, params)
	if err != nil {
		logger.Error("删除节点失败",
			logger.StringField("label", label),
			logger.StringField("id", id),
			logger.ErrorField(err))
		return err
	}

	logger.Info("删除节点成功",
		logger.StringField("label", label),
		logger.StringField("id", id))
	return nil
}

func (n *Neo4jManager) GetNode(ctx context.Context, label string, id string) (map[string]interface{}, error) {
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	params := map[string]interface{}{
		"id": id,
	}

	query := fmt.Sprintf(`
		MATCH (n:%s {id: $id})
		RETURN n
	`, label)

	result, err := session.Run(ctx, query, params)
	if err != nil {
		logger.Error("获取节点失败",
			logger.StringField("label", label),
			logger.StringField("id", id),
			logger.ErrorField(err))
		return nil, err
	}

	if result.Next(ctx) {
		record := result.Record()
		node, ok := record.Values[0].(neo4j.Node)
		if !ok {
			return nil, fmt.Errorf("failed to cast to node")
		}
		return node.Props, nil
	}

	return nil, nil
}

func (n *Neo4jManager) CreateRelationship(ctx context.Context, sourceLabel, sourceId, relType, targetLabel, targetId string, properties map[string]interface{}) error {
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	params := map[string]interface{}{
		"sourceId":   sourceId,
		"targetId":   targetId,
		"properties": properties,
	}

	query := fmt.Sprintf(`
		MATCH (a:%s {id: $sourceId}), (b:%s {id: $targetId})
		CREATE (a)-[r:%s]->(b)
		SET r += $properties
		RETURN r
	`, sourceLabel, targetLabel, relType)

	_, err := session.Run(ctx, query, params)
	if err != nil {
		logger.Error("创建关系失败",
			logger.StringField("sourceLabel", sourceLabel),
			logger.StringField("sourceId", sourceId),
			logger.StringField("relType", relType),
			logger.StringField("targetLabel", targetLabel),
			logger.StringField("targetId", targetId),
			logger.ErrorField(err))
		return err
	}

	logger.Info("创建关系成功",
		logger.StringField("sourceId", sourceId),
		logger.StringField("relType", relType),
		logger.StringField("targetId", targetId))
	return nil
}

func (n *Neo4jManager) DeleteRelationship(ctx context.Context, sourceLabel, sourceId, relType, targetLabel, targetId string) error {
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)

	params := map[string]interface{}{
		"sourceId": sourceId,
		"targetId": targetId,
	}

	query := fmt.Sprintf(`
		MATCH (a:%s {id: $sourceId})-[r:%s]->(b:%s {id: $targetId})
		DELETE r
	`, sourceLabel, relType, targetLabel)

	_, err := session.Run(ctx, query, params)
	if err != nil {
		logger.Error("删除关系失败",
			logger.StringField("sourceId", sourceId),
			logger.StringField("relType", relType),
			logger.StringField("targetId", targetId),
			logger.ErrorField(err))
		return err
	}

	logger.Info("删除关系成功",
		logger.StringField("sourceId", sourceId),
		logger.StringField("relType", relType),
		logger.StringField("targetId", targetId))
	return nil
}

func (n *Neo4jManager) GetRelatedNodes(ctx context.Context, label string, id string, relType string) ([]map[string]interface{}, error) {
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	params := map[string]interface{}{
		"id": id,
	}

	var query string
	if relType != "" {
		query = fmt.Sprintf(`
			MATCH (n:%s {id: $id})-[r:%s]-(m)
			RETURN DISTINCT m
		`, label, relType)
	} else {
		query = fmt.Sprintf(`
			MATCH (n:%s {id: $id})-[r]-(m)
			RETURN DISTINCT m
		`, label)
	}

	result, err := session.Run(ctx, query, params)
	if err != nil {
		logger.Error("获取相关节点失败",
			logger.StringField("label", label),
			logger.StringField("id", id),
			logger.StringField("relType", relType),
			logger.ErrorField(err))
		return nil, err
	}

	var nodes []map[string]interface{}
	for result.Next(ctx) {
		record := result.Record()
		node, ok := record.Values[0].(neo4j.Node)
		if ok {
			nodes = append(nodes, node.Props)
		}
	}

	return nodes, nil
}

func (n *Neo4jManager) GetGraphStats(ctx context.Context) (map[string]interface{}, error) {
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	stats := make(map[string]interface{})

	nodeCountResult, err := session.Run(ctx, "MATCH (n) RETURN count(n) as count", nil)
	if err != nil {
		return nil, err
	}
	if nodeCountResult.Next(ctx) {
		stats["nodeCount"] = nodeCountResult.Record().Values[0]
	}

	relCountResult, err := session.Run(ctx, "MATCH ()-[r]->() RETURN count(r) as count", nil)
	if err != nil {
		return nil, err
	}
	if relCountResult.Next(ctx) {
		stats["relationshipCount"] = relCountResult.Record().Values[0]
	}

	return stats, nil
}
