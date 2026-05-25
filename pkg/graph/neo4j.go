package graph

import (
	"context"
	"fmt"
	"sync"

	"Logos/config"
	"Logos/pkg/logger"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

var (
	neo4jDriver neo4j.DriverWithContext
	neo4jOnce   sync.Once
)

type Neo4jManager struct {
	driver neo4j.DriverWithContext
}

type SubgraphResult struct {
	Nodes         []map[string]interface{}
	Relationships []map[string]interface{}
}

type PathResult struct {
	Nodes         []map[string]interface{}
	Relationships []map[string]interface{}
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

func (n *Neo4jManager) GetSubgraph(ctx context.Context, label string, id string, depth int) (*SubgraphResult, error) {
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	if depth <= 0 {
		depth = 2
	}
	if depth > 4 {
		depth = 4
	}

	params := map[string]interface{}{
		"id":    id,
		"depth": depth,
	}

	query := fmt.Sprintf(`
		MATCH path = (n:%s {id: $id})-[*1..%d]-(m)
		RETURN nodes(path) as ns, relationships(path) as rs
	`, label, depth)

	result, err := session.Run(ctx, query, params)
	if err != nil {
		logger.Error("获取子图失败",
			logger.StringField("label", label),
			logger.StringField("id", id),
			logger.IntField("depth", depth),
			logger.ErrorField(err))
		return nil, err
	}

	subgraph := &SubgraphResult{}
	nodeSet := make(map[string]bool)
	relSet := make(map[string]bool)

	for result.Next(ctx) {
		record := result.Record()

		if nodes, ok := record.Values[0].([]interface{}); ok {
			for _, nodeVal := range nodes {
				if node, ok := nodeVal.(neo4j.Node); ok {
					nodeID, _ := node.Props["id"].(string)
					if nodeID != "" && !nodeSet[nodeID] {
						nodeSet[nodeID] = true
						subgraph.Nodes = append(subgraph.Nodes, node.Props)
					}
				}
			}
		}

		if rels, ok := record.Values[1].([]interface{}); ok {
			for _, relVal := range rels {
				if rel, ok := relVal.(neo4j.Relationship); ok {
					relKey := fmt.Sprintf("%s", rel.ElementId)
					if !relSet[relKey] {
						relSet[relKey] = true
						relProps := map[string]interface{}{
							"id":   rel.ElementId,
							"type": rel.Type,
						}
						for k, v := range rel.Props {
							relProps[k] = v
						}
						subgraph.Relationships = append(subgraph.Relationships, relProps)
					}
				}
			}
		}
	}

	logger.Info("获取子图成功",
		logger.StringField("id", id),
		logger.IntField("nodes", len(subgraph.Nodes)),
		logger.IntField("rels", len(subgraph.Relationships)))

	return subgraph, nil
}

func (n *Neo4jManager) GetShortestPath(ctx context.Context, sourceLabel, sourceId, targetLabel, targetId string, maxDepth int) ([]*PathResult, error) {
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	if maxDepth <= 0 {
		maxDepth = 4
	}
	if maxDepth > 6 {
		maxDepth = 6
	}

	params := map[string]interface{}{
		"sourceId": sourceId,
		"targetId": targetId,
		"maxDepth": maxDepth,
	}

	query := fmt.Sprintf(`
		MATCH (a:%s {id: $sourceId}), (b:%s {id: $targetId})
		MATCH path = shortestPath((a)-[*1..%d]-(b))
		RETURN nodes(path) as ns, relationships(path) as rs
		LIMIT 5
	`, sourceLabel, targetLabel, maxDepth)

	result, err := session.Run(ctx, query, params)
	if err != nil {
		logger.Error("获取最短路径失败",
			logger.StringField("sourceId", sourceId),
			logger.StringField("targetId", targetId),
			logger.ErrorField(err))
		return nil, err
	}

	var paths []*PathResult

	for result.Next(ctx) {
		record := result.Record()
		pathResult := &PathResult{}

		if nodes, ok := record.Values[0].([]interface{}); ok {
			for _, nodeVal := range nodes {
				if node, ok := nodeVal.(neo4j.Node); ok {
					pathResult.Nodes = append(pathResult.Nodes, node.Props)
				}
			}
		}

		if rels, ok := record.Values[1].([]interface{}); ok {
			for _, relVal := range rels {
				if rel, ok := relVal.(neo4j.Relationship); ok {
					relProps := map[string]interface{}{
						"id":   rel.ElementId,
						"type": rel.Type,
					}
					for k, v := range rel.Props {
						relProps[k] = v
					}
					pathResult.Relationships = append(pathResult.Relationships, relProps)
				}
			}
		}

		paths = append(paths, pathResult)
	}

	logger.Info("获取最短路径成功",
		logger.StringField("sourceId", sourceId),
		logger.StringField("targetId", targetId),
		logger.IntField("paths", len(paths)))

	return paths, nil
}

func (n *Neo4jManager) SearchNodes(ctx context.Context, keyword string, limit int) ([]map[string]interface{}, error) {
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	if limit <= 0 {
		limit = 20
	}

	params := map[string]interface{}{
		"keyword": fmt.Sprintf("(?i).*%s.*", keyword),
		"limit":   limit,
	}

	query := `
		MATCH (n)
		WHERE n.name =~ $keyword OR n.description =~ $keyword
		RETURN n
		LIMIT $limit
	`

	result, err := session.Run(ctx, query, params)
	if err != nil {
		logger.Error("搜索节点失败",
			logger.StringField("keyword", keyword),
			logger.ErrorField(err))
		return nil, err
	}

	var nodes []map[string]interface{}
	for result.Next(ctx) {
		record := result.Record()
		if node, ok := record.Values[0].(neo4j.Node); ok {
			nodes = append(nodes, node.Props)
		}
	}

	return nodes, nil
}

func (n *Neo4jManager) GetTypeStats(ctx context.Context) (map[string]int64, error) {
	session := n.driver.NewSession(ctx, neo4j.SessionConfig{AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)

	result, err := session.Run(ctx, `
		MATCH (n)
		RETURN labels(n)[0] as label, count(n) as count
		ORDER BY count DESC
	`, nil)
	if err != nil {
		return nil, err
	}

	stats := make(map[string]int64)
	for result.Next(ctx) {
		record := result.Record()
		label, _ := record.Values[0].(string)
		if count, ok := record.Values[1].(int64); ok && label != "" {
			stats[label] = count
		}
	}

	return stats, nil
}
