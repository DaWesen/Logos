package http

import (
	"html/template"
	"net/http"

	"github.com/gin-gonic/gin"
)

type ToolLink struct {
	Name        string
	URL         string
	Description string
	Category    string
	Icon        string
}

type MonitoringHTTPHandler struct {
	tools []ToolLink
}

func NewMonitoringHTTPHandler() *MonitoringHTTPHandler {
	tools := []ToolLink{
		{Name: "Grafana", URL: "http://localhost:3000", Description: "指标可视化与监控仪表盘", Category: "可观测性", Icon: "📊"},
		{Name: "Jaeger", URL: "http://localhost:16686", Description: "分布式链路追踪 UI", Category: "可观测性", Icon: "🔍"},
		{Name: "Kiali", URL: "http://localhost:20001", Description: "Istio 服务网格可视化", Category: "可观测性", Icon: "🕸️"},
		{Name: "Prometheus", URL: "http://localhost:9090", Description: "指标采集与查询", Category: "可观测性", Icon: "🔥"},
		{Name: "MinIO Console", URL: "http://localhost:9001", Description: "对象存储管理（minioadmin / minioadmin123）", Category: "存储", Icon: "📦"},
		{Name: "Elasticsearch", URL: "http://localhost:9200", Description: "全文搜索引擎 API", Category: "存储", Icon: "🔎"},
		{Name: "Neo4j Browser", URL: "http://localhost:7474", Description: "知识图谱可视化（neo4j / neo4j123456）", Category: "存储", Icon: "🧠"},
		{Name: "Milvus Attu", URL: "http://localhost:9091", Description: "向量数据库管理", Category: "存储", Icon: "🎯"},
		{Name: "Kafka UI", URL: "http://localhost:8080", Description: "消息队列管理", Category: "中间件", Icon: "📨"},
		{Name: "etcd", URL: "http://localhost:2379", Description: "服务注册与发现", Category: "中间件", Icon: "🔑"},
		{Name: "RedisInsight", URL: "http://localhost:8001", Description: "Redis 缓存管理", Category: "中间件", Icon: "⚡"},
	}

	return &MonitoringHTTPHandler{tools: tools}
}

func (h *MonitoringHTTPHandler) RegisterRoutes(r *gin.Engine) {
	r.GET("/", h.Dashboard)
	r.GET("/health", h.HealthCheck)
}

func (h *MonitoringHTTPHandler) HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "monitoring"})
}

func (h *MonitoringHTTPHandler) Dashboard(c *gin.Context) {
	tmpl := template.Must(template.New("dashboard").Parse(dashboardHTML))
	c.Header("Content-Type", "text/html; charset=utf-8")

	categories := make(map[string][]ToolLink)
	for _, t := range h.tools {
		categories[t.Category] = append(categories[t.Category], t)
	}

	data := struct {
		Categories map[string][]ToolLink
		Total      int
	}{
		Categories: categories,
		Total:      len(h.tools),
	}

	if err := tmpl.Execute(c.Writer, data); err != nil {
		c.String(http.StatusInternalServerError, "render error: %v", err)
	}
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Logos 运维工具导航</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            background: #0f172a;
            color: #e2e8f0;
            min-height: 100vh;
        }
        .header {
            background: linear-gradient(135deg, #1e293b 0%, #0f172a 100%);
            border-bottom: 1px solid #1e293b;
            padding: 2rem;
            text-align: center;
        }
        .header h1 {
            font-size: 2rem;
            background: linear-gradient(135deg, #60a5fa, #a78bfa);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
            margin-bottom: 0.5rem;
        }
        .header p { color: #94a3b8; font-size: 0.95rem; }
        .container { max-width: 1200px; margin: 0 auto; padding: 2rem; }
        .category { margin-bottom: 2.5rem; }
        .category-title {
            font-size: 1.1rem;
            color: #94a3b8;
            text-transform: uppercase;
            letter-spacing: 0.1em;
            margin-bottom: 1rem;
            padding-bottom: 0.5rem;
            border-bottom: 1px solid #1e293b;
        }
        .grid {
            display: grid;
            grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
            gap: 1rem;
        }
        .card {
            background: #1e293b;
            border: 1px solid #334155;
            border-radius: 12px;
            padding: 1.5rem;
            text-decoration: none;
            color: inherit;
            transition: all 0.2s ease;
            display: flex;
            align-items: flex-start;
            gap: 1rem;
        }
        .card:hover {
            border-color: #60a5fa;
            transform: translateY(-2px);
            box-shadow: 0 4px 20px rgba(96, 165, 250, 0.1);
        }
        .card-icon {
            font-size: 2rem;
            flex-shrink: 0;
            width: 48px;
            height: 48px;
            display: flex;
            align-items: center;
            justify-content: center;
            background: #0f172a;
            border-radius: 10px;
        }
        .card-content { flex: 1; }
        .card-name {
            font-size: 1.05rem;
            font-weight: 600;
            color: #f1f5f9;
            margin-bottom: 0.3rem;
        }
        .card-desc {
            font-size: 0.85rem;
            color: #94a3b8;
            line-height: 1.4;
        }
        .card-url {
            font-size: 0.75rem;
            color: #60a5fa;
            margin-top: 0.5rem;
            word-break: break-all;
        }
        .footer {
            text-align: center;
            padding: 2rem;
            color: #475569;
            font-size: 0.85rem;
        }
        .stats {
            display: flex;
            justify-content: center;
            gap: 2rem;
            margin-top: 1rem;
        }
        .stat { text-align: center; }
        .stat-value { font-size: 1.5rem; font-weight: 700; color: #60a5fa; }
        .stat-label { font-size: 0.8rem; color: #64748b; }
    </style>
</head>
<body>
    <div class="header">
        <h1>🛠️ Logos 运维工具导航</h1>
        <p>基础设施与可观测性工具入口</p>
        <div class="stats">
            <div class="stat">
                <div class="stat-value">{{.Total}}</div>
                <div class="stat-label">工具总数</div>
            </div>
            <div class="stat">
                <div class="stat-value">{{len .Categories}}</div>
                <div class="stat-label">分类数</div>
            </div>
        </div>
    </div>
    <div class="container">
        {{range $cat, $tools := .Categories}}
        <div class="category">
            <div class="category-title">{{$cat}}</div>
            <div class="grid">
                {{range $tools}}
                <a class="card" href="{{.URL}}" target="_blank" rel="noopener">
                    <div class="card-icon">{{.Icon}}</div>
                    <div class="card-content">
                        <div class="card-name">{{.Name}}</div>
                        <div class="card-desc">{{.Description}}</div>
                        <div class="card-url">{{.URL}}</div>
                    </div>
                </a>
                {{end}}
            </div>
        </div>
        {{end}}
    </div>
    <div class="footer">
        Logos Platform &copy; 2026 &mdash; Monitoring Dashboard
    </div>
</body>
</html>`
