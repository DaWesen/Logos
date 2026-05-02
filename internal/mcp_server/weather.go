package mcp_server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type WeatherTool struct{}

func (t *WeatherTool) Name() string        { return "weather" }
func (t *WeatherTool) Description() string { return "查询指定城市的天气信息" }
func (t *WeatherTool) Type() int           { return 3 }
func (t *WeatherTool) Parameters() []ToolParamDef {
	return []ToolParamDef{
		{Name: "location", Type: "string", Description: "城市名称，如 Beijing/Tokyo/New York", Required: true},
		{Name: "units", Type: "string", Description: "温度单位: metric(摄氏)/imperial(华氏)", Required: false, DefaultValue: "metric"},
	}
}

type wttrResponse struct {
	CurrentCondition []struct {
		FeelsLikeC     string `json:"FeelsLikeC"`
		FeelsLikeF     string `json:"FeelsLikeF"`
		TempC          string `json:"temp_C"`
		TempF          string `json:"temp_F"`
		Humidity       string `json:"humidity"`
		WeatherDesc    []struct {
			Value string `json:"value"`
		} `json:"weatherDesc"`
		Winddir16Point string `json:"winddir16Point"`
		WindspeedKmph  string `json:"windspeedKmph"`
		Visibility     string `json:"visibility"`
		Pressure       string `json:"pressure"`
		UVIndex        string `json:"uvIndex"`
		Cloudcover     string `json:"cloudcover"`
	} `json:"current_condition"`
	NearestArea []struct {
		AreaName []struct {
			Value string `json:"value"`
		} `json:"areaName"`
		Country []struct {
			Value string `json:"value"`
		} `json:"country"`
		Region []struct {
			Value string `json:"value"`
		} `json:"region"`
		Lat     string `json:"latitude"`
		Lon     string `json:"longitude"`
	} `json:"nearest_area"`
}

func (t *WeatherTool) Execute(ctx context.Context, params map[string]string) (*ToolResult, error) {
	location := params["location"]
	if location == "" {
		return &ToolResult{Content: "缺少location参数", IsError: true}, nil
	}

	units := params["units"]
	if units == "" {
		units = "metric"
	}

	result, err := queryWeather(ctx, location)
	if err != nil {
		return &ToolResult{
			Content:  fmt.Sprintf("天气查询失败: %s", err.Error()),
			IsError:  true,
			Metadata: map[string]string{"location": location},
		}, nil
	}

	var sb strings.Builder
	var tempStr, feelsLikeStr string
	if units == "imperial" {
		tempStr = fmt.Sprintf("%s°F", result.CurrentCondition[0].TempF)
		feelsLikeStr = fmt.Sprintf("%s°F", result.CurrentCondition[0].FeelsLikeF)
	} else {
		tempStr = fmt.Sprintf("%s°C", result.CurrentCondition[0].TempC)
		feelsLikeStr = fmt.Sprintf("%s°C", result.CurrentCondition[0].FeelsLikeC)
	}

	areaName := location
	countryName := ""
	if len(result.NearestArea) > 0 {
		if len(result.NearestArea[0].AreaName) > 0 {
			areaName = result.NearestArea[0].AreaName[0].Value
		}
		if len(result.NearestArea[0].Country) > 0 {
			countryName = result.NearestArea[0].Country[0].Value
		}
	}

	weatherDesc := ""
	if len(result.CurrentCondition[0].WeatherDesc) > 0 {
		weatherDesc = result.CurrentCondition[0].WeatherDesc[0].Value
	}

	sb.WriteString(fmt.Sprintf("📍 %s", areaName))
	if countryName != "" {
		sb.WriteString(fmt.Sprintf(", %s", countryName))
	}
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("🌡️ 温度: %s (体感: %s)\n", tempStr, feelsLikeStr))
	sb.WriteString(fmt.Sprintf("🌤️ 天气: %s\n", weatherDesc))
	sb.WriteString(fmt.Sprintf("💧 湿度: %s%%\n", result.CurrentCondition[0].Humidity))
	sb.WriteString(fmt.Sprintf("💨 风向: %s, 风速: %skm/h\n", result.CurrentCondition[0].Winddir16Point, result.CurrentCondition[0].WindspeedKmph))
	sb.WriteString(fmt.Sprintf("👁️ 能见度: %skm\n", result.CurrentCondition[0].Visibility))
	sb.WriteString(fmt.Sprintf("📊 气压: %shPa\n", result.CurrentCondition[0].Pressure))
	sb.WriteString(fmt.Sprintf("☁️ 云量: %s%%\n", result.CurrentCondition[0].Cloudcover))
	sb.WriteString(fmt.Sprintf("☀️ UV指数: %s\n", result.CurrentCondition[0].UVIndex))

	metadata := map[string]string{
		"location":  areaName,
		"country":   countryName,
		"temp_c":    result.CurrentCondition[0].TempC,
		"temp_f":    result.CurrentCondition[0].TempF,
		"humidity":  result.CurrentCondition[0].Humidity,
		"weather":   weatherDesc,
		"source":    "wttr.in",
		"units":     units,
	}

	metadataJSON, _ := json.Marshal(metadata)
	metadata["raw"] = string(metadataJSON)

	return &ToolResult{
		Content:  sb.String(),
		Metadata: metadata,
	}, nil
}

func queryWeather(ctx context.Context, location string) (*wttrResponse, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	url := fmt.Sprintf("https://wttr.in/%s?format=j1", location)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("User-Agent", "Logos-AIM-MCP/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API返回状态码: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	var result wttrResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	if len(result.CurrentCondition) == 0 {
		return nil, fmt.Errorf("未获取到天气数据")
	}

	return &result, nil
}
