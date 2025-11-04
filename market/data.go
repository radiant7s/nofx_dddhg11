package market

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// Data 市场数据结构
type Data struct {
	Symbol            string
	CurrentPrice      float64
	PriceChange1h     float64 // 1小时价格变化百分比
	PriceChange4h     float64 // 4小时价格变化百分比
	PriceChange1d     float64 // 日线价格变化百分比
	CurrentEMA20      float64
	CurrentMACD       float64
	CurrentRSI7       float64
	OpenInterest      *OIData
	FundingRate       float64
	MultiTimeframe    *MultiTimeframeData
	LongerTermContext *LongerTermData
	MarketStructure   *MarketStructure // 新增：市场结构分析
	FibLevels         *FibLevels       // 新增：斐波那契水平
}

// ==================== 新增：斐波那契和市场结构相关结构 ====================

// FibLevels 斐波那契水平
type FibLevels struct {
    Level236 float64 // 0.236
    Level382 float64 // 0.382
    Level500 float64 // 0.5
    Level618 float64 // 0.618
    Level705 float64 // 0.705
    Level786 float64 // 0.786
    High     float64 // 波段高点
    Low      float64 // 波段低点
    Trend    string  // "bullish" or "bearish"
}

// MarketStructure 市场结构
type MarketStructure struct {
    SwingHighs []float64 // 波段高点序列
    SwingLows  []float64 // 波段低点序列
    CurrentBias string   // "bullish", "bearish", "neutral"
    FibLevels  *FibLevels
}

// OIData Open Interest数据
type OIData struct {
	Latest  float64
	Average float64
}

// MultiTimeframeData 多时间框架数据
type MultiTimeframeData struct {
	Timeframe15m *TimeframeData
	Timeframe1h  *TimeframeData
	Timeframe4h  *TimeframeData
	Timeframe1d  *TimeframeData // 新增：日线数据
}

// TimeframeData 单个时间框架数据
type TimeframeData struct {
	Timeframe      string
	CurrentPrice   float64
	EMA20          float64
	EMA50          float64
	MACD           float64
	RSI7           float64
	RSI14          float64
	ATR14          float64
	Volume         float64
	PriceSeries    []float64
	TrendDirection string // "bullish", "bearish", "neutral"
	SignalStrength int    // 0-100
}

// LongerTermData 长期数据
type LongerTermData struct {
	EMA20         float64
	EMA50         float64
	ATR3          float64
	ATR14         float64
	CurrentVolume float64
	AverageVolume float64
	MACDValues    []float64
	RSI14Values   []float64
}

// Kline K线数据
type Kline struct {
	OpenTime  int64
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	CloseTime int64
}

// ==================== 新增：斐波那契计算函数 ====================

// calculateFibonacciLevels 计算斐波那契回撤水平
func calculateFibonacciLevels(high, low float64) *FibLevels {
    diff := high - low
    return &FibLevels{
        Level236: high - (diff * 0.236),
        Level382: high - (diff * 0.382),
        Level500: high - (diff * 0.5),
        Level618: high - (diff * 0.618),
        Level705: high - (diff * 0.705),
        Level786: high - (diff * 0.786),
        High:     high,
        Low:      low,
        Trend:    "bullish", // 默认，实际使用时需要根据趋势判断
    }
}

// detectMarketStructure 检测市场结构
func detectMarketStructure(priceSeries []float64) *MarketStructure {
    if len(priceSeries) < 10 {
        return nil
    }

    structure := &MarketStructure{
        SwingHighs: make([]float64, 0),
        SwingLows:  make([]float64, 0),
    }

    // 简单的波段检测算法
    for i := 2; i < len(priceSeries)-2; i++ {
        // 检测波段高点
        if priceSeries[i] > priceSeries[i-1] && priceSeries[i] > priceSeries[i-2] &&
           priceSeries[i] > priceSeries[i+1] && priceSeries[i] > priceSeries[i+2] {
            structure.SwingHighs = append(structure.SwingHighs, priceSeries[i])
        }
        // 检测波段低点
        if priceSeries[i] < priceSeries[i-1] && priceSeries[i] < priceSeries[i-2] &&
           priceSeries[i] < priceSeries[i+1] && priceSeries[i] < priceSeries[i+2] {
            structure.SwingLows = append(structure.SwingLows, priceSeries[i])
        }
    }

    // 确定当前偏向
    if len(structure.SwingHighs) > 1 && len(structure.SwingLows) > 1 {
        latestHigh := structure.SwingHighs[len(structure.SwingHighs)-1]
        prevHigh := structure.SwingHighs[len(structure.SwingHighs)-2]
        latestLow := structure.SwingLows[len(structure.SwingLows)-1]
        prevLow := structure.SwingLows[len(structure.SwingLows)-2]

        if latestHigh > prevHigh && latestLow > prevLow {
            structure.CurrentBias = "bullish"
        } else if latestHigh < prevHigh && latestLow < prevLow {
            structure.CurrentBias = "bearish"
        } else {
            structure.CurrentBias = "neutral"
        }
    }

    return structure
}

// calculateCurrentFibLevels 计算当前斐波那契水平
func calculateCurrentFibLevels(structure *MarketStructure) *FibLevels {
    if structure == nil || len(structure.SwingHighs) < 2 || len(structure.SwingLows) < 2 {
        return nil
    }

    // 使用最近的波段高点和低点
    recentHigh := structure.SwingHighs[len(structure.SwingHighs)-1]
    recentLow := structure.SwingLows[len(structure.SwingLows)-1]

    // 确保高点高于低点
    if recentHigh <= recentLow {
        return nil
    }

    fibLevels := calculateFibonacciLevels(recentHigh, recentLow)
    fibLevels.Trend = structure.CurrentBias
    
    return fibLevels
}

// ==================== 震荡市检测相关结构 ====================

// MarketCondition 市场状态结构
type MarketCondition struct {
	Condition    string  // "trending", "ranging", "volatile"
	Confidence   int     // 0-100
	ATRRatio     float64 // ATR/Price 比率
	EMASlope     float64 // EMA20斜率
	PriceChannel float64 // 价格通道宽度
}

// DetectMarketCondition 检测市场状态（新增函数）
func DetectMarketCondition(data *Data) *MarketCondition {
	if data == nil {
		return &MarketCondition{Condition: "unknown", Confidence: 0}
	}

	condition := &MarketCondition{}

	// 使用现有数据计算市场状态
	atrRatio := calculateATRRatio(data)
	emaSlope := calculateEMASlope(data)
	priceChannel := calculatePriceChannel(data)
	rsiPosition := analyzeRSIPosition(data)
	timeframeConsistency := checkTimeframeConsistency(data)

	trendingScore, rangingScore := calculateMarketScores(
		atrRatio, emaSlope, priceChannel, rsiPosition, timeframeConsistency)

	if trendingScore > 70 {
		condition.Condition = "trending"
		condition.Confidence = trendingScore
	} else if rangingScore > 60 {
		condition.Condition = "ranging"
		condition.Confidence = rangingScore
	} else {
		condition.Condition = "volatile"
		condition.Confidence = 50
	}

	condition.ATRRatio = atrRatio
	condition.EMASlope = emaSlope
	condition.PriceChannel = priceChannel

	return condition
}

// calculateATRRatio 基于现有ATR数据计算波动率
func calculateATRRatio(data *Data) float64 {
	if data.LongerTermContext == nil || data.CurrentPrice == 0 {
		return 0
	}
	return (data.LongerTermContext.ATR14 / data.CurrentPrice) * 100
}

// calculateEMASlope 基于现有EMA数据计算斜率
func calculateEMASlope(data *Data) float64 {
	// 方法1：使用多时间框架EMA值估算斜率
	if data.MultiTimeframe != nil {
		var emaValues []float64
		if data.MultiTimeframe.Timeframe15m != nil {
			emaValues = append(emaValues, data.MultiTimeframe.Timeframe15m.EMA20)
		}
		if data.MultiTimeframe.Timeframe1h != nil {
			emaValues = append(emaValues, data.MultiTimeframe.Timeframe1h.EMA20)
		}
		if data.MultiTimeframe.Timeframe4h != nil {
			emaValues = append(emaValues, data.MultiTimeframe.Timeframe4h.EMA20)
		}
		if data.MultiTimeframe.Timeframe1d != nil {
			emaValues = append(emaValues, data.MultiTimeframe.Timeframe1d.EMA20)
		}

		if len(emaValues) >= 2 {
			// 计算EMA变化的百分比斜率
			slope := (emaValues[len(emaValues)-1] - emaValues[0]) / emaValues[0] * 100
			return slope
		}
	}

	// 方法2：使用当前EMA和历史EMA（如果有）
	if data.LongerTermContext != nil && data.LongerTermContext.EMA20 != 0 {
		slope := (data.CurrentEMA20 - data.LongerTermContext.EMA20) / data.LongerTermContext.EMA20 * 100
		return slope
	}

	return 0
}

// calculatePriceChannel 计算价格通道宽度
func calculatePriceChannel(data *Data) float64 {
	// 使用多时间框架的最高最低EMA估算通道
	if data.MultiTimeframe == nil {
		return 0
	}

	var emas []float64
	if data.MultiTimeframe.Timeframe15m != nil {
		emas = append(emas, data.MultiTimeframe.Timeframe15m.EMA20)
	}
	if data.MultiTimeframe.Timeframe1h != nil {
		emas = append(emas, data.MultiTimeframe.Timeframe1h.EMA20)
	}
	if data.MultiTimeframe.Timeframe4h != nil {
		emas = append(emas, data.MultiTimeframe.Timeframe4h.EMA20)
	}
	if data.MultiTimeframe.Timeframe1d != nil {
		emas = append(emas, data.MultiTimeframe.Timeframe1d.EMA20)
	}

	if len(emas) < 2 {
		return 0
	}

	// 找到EMA的最大最小值
	minEMA, maxEMA := emas[0], emas[0]
	for _, ema := range emas {
		if ema < minEMA {
			minEMA = ema
		}
		if ema > maxEMA {
			maxEMA = ema
		}
	}

	channelWidth := (maxEMA - minEMA) / data.CurrentPrice * 100
	return channelWidth
}

// analyzeRSIPosition 分析RSI位置
func analyzeRSIPosition(data *Data) float64 {
	// 使用现有RSI数据判断是否在震荡区间
	rsiValue := data.CurrentRSI7

	// 判断RSI是否在震荡区间 (30-70)
	if rsiValue >= 30 && rsiValue <= 70 {
		return 80 // 高概率震荡
	} else if rsiValue >= 40 && rsiValue <= 60 {
		return 95 // 极高概率震荡
	} else {
		return 30 // 低概率震荡
	}
}

// checkTimeframeConsistency 检查多时间框架一致性
func checkTimeframeConsistency(data *Data) float64 {
	if data.MultiTimeframe == nil {
		return 0
	}

	timeframes := []*TimeframeData{
		data.MultiTimeframe.Timeframe15m,
		data.MultiTimeframe.Timeframe1h,
		data.MultiTimeframe.Timeframe4h,
		data.MultiTimeframe.Timeframe1d, // 新增：包含日线
	}

	bullishCount, bearishCount := 0, 0
	validCount := 0

	for _, tf := range timeframes {
		if tf != nil {
			validCount++
			if tf.TrendDirection == "bullish" {
				bullishCount++
			} else if tf.TrendDirection == "bearish" {
				bearishCount++
			}
		}
	}

	if validCount == 0 {
		return 0
	}

	// 计算一致性得分
	consistency := math.Max(float64(bullishCount), float64(bearishCount)) / float64(validCount) * 100
	return consistency
}

// calculateMarketScores 计算市场状态得分
func calculateMarketScores(atrRatio, emaSlope, priceChannel, rsiPosition, timeframeConsistency float64) (int, int) {
	trendingScore, rangingScore := 0, 0

	// 趋势市特征
	if math.Abs(emaSlope) > 0.1 { // EMA有明显斜率
		trendingScore += 25
	}
	if atrRatio > 0.3 { // 波动率适中偏高
		trendingScore += 20
	}
	if timeframeConsistency > 70 { // 多时间框架一致
		trendingScore += 30
	}
	if rsiPosition < 50 { // RSI不在中间区域
		trendingScore += 25
	}

	// 震荡市特征
	if math.Abs(emaSlope) < 0.05 { // EMA走平
		rangingScore += 30
	}
	if priceChannel < 2.0 { // 价格通道狭窄
		rangingScore += 25
	}
	if rsiPosition > 70 { // RSI常在中间区域
		rangingScore += 25
	}
	if timeframeConsistency < 50 { // 多时间框架不一致
		rangingScore += 20
	}

	return trendingScore, rangingScore
}

// IsRangingMarket 判断是否为震荡市（新增函数）
func IsRangingMarket(data *Data) bool {
	condition := DetectMarketCondition(data)
	return condition.Condition == "ranging" && condition.Confidence > 60
}

// ==================== 原有函数保持不变 ====================

// Get 获取指定代币的市场数据
func Get(symbol string) (*Data, error) {
	// 标准化symbol
	symbol = Normalize(symbol)

	// 获取多时间框架数据
	multiTimeframe, err := getMultiTimeframeData(symbol)
	if err != nil {
		return nil, fmt.Errorf("获取多时间框架数据失败: %v", err)
	}

	// 使用15分钟作为主要参考时间框架
	primaryData := multiTimeframe.Timeframe15m

	// 计算价格变化百分比
	priceChange1h := calculatePriceChange(multiTimeframe.Timeframe1h.PriceSeries)
	priceChange4h := calculatePriceChange(multiTimeframe.Timeframe4h.PriceSeries)
	priceChange1d := calculatePriceChange(multiTimeframe.Timeframe1d.PriceSeries)

	// 获取OI数据
	oiData, err := getOpenInterestData(symbol)
	if err != nil {
		oiData = &OIData{Latest: 0, Average: 0}
	}

	// 获取Funding Rate
	fundingRate, _ := getFundingRate(symbol)

	// 计算长期数据 (基于4小时)
	longerTermData := calculateLongerTermData(multiTimeframe.Timeframe4h.PriceSeries, multiTimeframe.Timeframe4h.Volume)

	// 计算市场结构和斐波那契水平（使用日线数据）
	var marketStructure *MarketStructure
	var fibLevels *FibLevels
	
	if multiTimeframe.Timeframe1d != nil {
		marketStructure = detectMarketStructure(multiTimeframe.Timeframe1d.PriceSeries)
		if marketStructure != nil {
			fibLevels = calculateCurrentFibLevels(marketStructure)
		}
	}

	return &Data{
		Symbol:            symbol,
		CurrentPrice:      primaryData.CurrentPrice,
		PriceChange1h:     priceChange1h,
		PriceChange4h:     priceChange4h,
		PriceChange1d:     priceChange1d, // 新增：日线价格变化
		CurrentEMA20:      primaryData.EMA20,
		CurrentMACD:       primaryData.MACD,
		CurrentRSI7:       primaryData.RSI7,
		OpenInterest:      oiData,
		FundingRate:       fundingRate,
		MultiTimeframe:    multiTimeframe,
		LongerTermContext: longerTermData,
		MarketStructure:   marketStructure, // 新增
		FibLevels:         fibLevels,       // 新增
	}, nil
}

// getMultiTimeframeData 获取多时间框架数据
func getMultiTimeframeData(symbol string) (*MultiTimeframeData, error) {
	data := &MultiTimeframeData{}

	// 获取15分钟数据 (主要交易框架)
	klines15m, err := getKlines(symbol, "15m", 40)
	if err != nil {
		return nil, fmt.Errorf("获取15分钟K线失败: %v", err)
	}
	data.Timeframe15m = calculateTimeframeData(klines15m, "15m")

	// 获取1小时数据 (趋势确认)
	klines1h, err := getKlines(symbol, "1h", 50)
	if err != nil {
		return nil, fmt.Errorf("获取1小时K线失败: %v", err)
	}
	data.Timeframe1h = calculateTimeframeData(klines1h, "1h")

	// 获取4小时数据 (大方向判断)
	klines4h, err := getKlines(symbol, "4h", 60)
	if err != nil {
		return nil, fmt.Errorf("获取4小时K线失败: %v", err)
	}
	data.Timeframe4h = calculateTimeframeData(klines4h, "4h")

	// 获取日线数据 (长期趋势)
	klines1d, err := getKlines(symbol, "1d", 90) // 获取90天日线数据
	if err != nil {
		return nil, fmt.Errorf("获取日线K线失败: %v", err)
	}
	data.Timeframe1d = calculateTimeframeData(klines1d, "1d")

	return data, nil
}

// calculateTimeframeData 计算单个时间框架数据
func calculateTimeframeData(klines []Kline, timeframe string) *TimeframeData {
	if len(klines) == 0 {
		return &TimeframeData{Timeframe: timeframe}
	}

	currentPrice := klines[len(klines)-1].Close
	
	// 提取价格序列
	priceSeries := make([]float64, len(klines))
	for i, k := range klines {
		priceSeries[i] = k.Close
	}

	// 计算技术指标
	ema20 := calculateEMAFromSeries(priceSeries, 20)
	ema50 := calculateEMAFromSeries(priceSeries, 50)
	macd := calculateMACDFromSeries(priceSeries)
	rsi7 := calculateRSIFromSeries(priceSeries, 7)
	rsi14 := calculateRSIFromSeries(priceSeries, 14)
	atr14 := calculateATRFromKlines(klines, 14)
	
	volume := 0.0
	if len(klines) > 0 {
		volume = klines[len(klines)-1].Volume
	}

	// 判断趋势方向
	trendDirection := determineTrendDirection(currentPrice, ema20, ema50, macd)
	
	// 计算信号强度
	signalStrength := calculateTimeframeSignalStrength(currentPrice, ema20, ema50, macd, rsi7)

	return &TimeframeData{
		Timeframe:      timeframe,
		CurrentPrice:   currentPrice,
		EMA20:          ema20,
		EMA50:          ema50,
		MACD:           macd,
		RSI7:           rsi7,
		RSI14:          rsi14,
		ATR14:          atr14,
		Volume:         volume,
		PriceSeries:    priceSeries,
		TrendDirection: trendDirection,
		SignalStrength: signalStrength,
	}
}

// determineTrendDirection 判断趋势方向
func determineTrendDirection(price, ema20, ema50, macd float64) string {
	bullishSignals := 0
	bearishSignals := 0

	if price > ema20 && ema20 > 0 {
		bullishSignals++
	} else if price < ema20 && ema20 > 0 {
		bearishSignals++
	}

	if ema20 > ema50 && ema50 > 0 {
		bullishSignals++
	} else if ema20 < ema50 && ema50 > 0 {
		bearishSignals++
	}

	if macd > 0.001 {
		bullishSignals++
	} else if macd < -0.001 {
		bearishSignals++
	}

	if bullishSignals >= 2 {
		return "bullish"
	} else if bearishSignals >= 2 {
		return "bearish"
	}
	return "neutral"
}

// calculateTimeframeSignalStrength 计算时间框架信号强度
func calculateTimeframeSignalStrength(price, ema20, ema50, macd, rsi7 float64) int {
	strength := 50

	// 价格与EMA关系
	if price > ema20 && ema20 > ema50 {
		strength += 20
	} else if price < ema20 && ema20 < ema50 {
		strength -= 20
	}

	// MACD信号
	if macd > 0.001 {
		strength += 15
	} else if macd < -0.001 {
		strength -= 15
	}

	// RSI信号
	if rsi7 < 30 {
		strength += 10
	} else if rsi7 > 70 {
		strength -= 10
	}

	if strength < 0 {
		return 0
	}
	if strength > 100 {
		return 100
	}
	return strength
}

// calculatePriceChange 计算价格变化百分比
func calculatePriceChange(priceSeries []float64) float64 {
	if len(priceSeries) < 2 {
		return 0
	}
	current := priceSeries[len(priceSeries)-1]
	previous := priceSeries[0]
	if previous > 0 {
		return ((current - previous) / previous) * 100
	}
	return 0
}

// calculateEMAFromSeries 计算EMA (基于价格序列)
func calculateEMAFromSeries(prices []float64, period int) float64 {
	if len(prices) < period {
		return 0
	}

	// 计算SMA作为初始EMA
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += prices[i]
	}
	ema := sum / float64(period)

	// 计算EMA
	multiplier := 2.0 / float64(period+1)
	for i := period; i < len(prices); i++ {
		ema = (prices[i]-ema)*multiplier + ema
	}

	return ema
}

// calculateMACDFromSeries 从价格序列计算MACD
func calculateMACDFromSeries(prices []float64) float64 {
	if len(prices) < 26 {
		return 0
	}

	ema12 := calculateEMAFromSeries(prices, 12)
	ema26 := calculateEMAFromSeries(prices, 26)

	return ema12 - ema26
}

// calculateRSIFromSeries 从价格序列计算RSI
func calculateRSIFromSeries(prices []float64, period int) float64 {
	if len(prices) <= period {
		return 0
	}

	gains := 0.0
	losses := 0.0

	for i := 1; i <= period; i++ {
		change := prices[i] - prices[i-1]
		if change > 0 {
			gains += change
		} else {
			losses += -change
		}
	}

	avgGain := gains / float64(period)
	avgLoss := losses / float64(period)

	if avgLoss == 0 {
		return 100
	}

	rs := avgGain / avgLoss
	return 100 - (100 / (1 + rs))
}

// calculateATRFromKlines 从K线计算ATR
func calculateATRFromKlines(klines []Kline, period int) float64 {
	if len(klines) <= period {
		return 0
	}

	trs := make([]float64, len(klines))
	for i := 1; i < len(klines); i++ {
		high := klines[i].High
		low := klines[i].Low
		prevClose := klines[i-1].Close

		tr1 := high - low
		tr2 := math.Abs(high - prevClose)
		tr3 := math.Abs(low - prevClose)

		trs[i] = math.Max(tr1, math.Max(tr2, tr3))
	}

	sum := 0.0
	for i := 1; i <= period; i++ {
		sum += trs[i]
	}
	atr := sum / float64(period)

	for i := period + 1; i < len(klines); i++ {
		atr = (atr*float64(period-1) + trs[i]) / float64(period)
	}

	return atr
}

// calculateLongerTermData 计算长期数据
func calculateLongerTermData(priceSeries []float64, volume float64) *LongerTermData {
	data := &LongerTermData{
		MACDValues:  make([]float64, 0, 10),
		RSI14Values: make([]float64, 0, 10),
	}

	if len(priceSeries) == 0 {
		return data
	}

	// 计算EMA
	data.EMA20 = calculateEMAFromSeries(priceSeries, 20)
	data.EMA50 = calculateEMAFromSeries(priceSeries, 50)

	// 计算ATR (简化版本)
	data.ATR14 = calculateSimpleATR(priceSeries, 14)
	data.ATR3 = calculateSimpleATR(priceSeries, 3)

	// 成交量数据
	data.CurrentVolume = volume
	data.AverageVolume = volume // 简化处理

	// 计算MACD和RSI序列
	start := len(priceSeries) - 10
	if start < 0 {
		start = 0
	}

	for i := start; i < len(priceSeries); i++ {
		if i >= 26 {
			macd := calculateMACDFromSeries(priceSeries[:i+1])
			data.MACDValues = append(data.MACDValues, macd)
		}
		if i >= 14 {
			rsi14 := calculateRSIFromSeries(priceSeries[:i+1], 14)
			data.RSI14Values = append(data.RSI14Values, rsi14)
		}
	}

	return data
}

// calculateSimpleATR 简化版ATR计算
func calculateSimpleATR(prices []float64, period int) float64 {
	if len(prices) < period {
		return 0
	}

	sum := 0.0
	for i := 1; i <= period; i++ {
		tr := math.Abs(prices[i] - prices[i-1])
		sum += tr
	}

	return sum / float64(period)
}

// getKlines 从Binance获取K线数据
func getKlines(symbol, interval string, limit int) ([]Kline, error) {
	url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/klines?symbol=%s&interval=%s&limit=%d",
		symbol, interval, limit)

	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rawData [][]interface{}
	if err := json.Unmarshal(body, &rawData); err != nil {
		return nil, err
	}

	klines := make([]Kline, len(rawData))
	for i, item := range rawData {
		openTime := int64(item[0].(float64))
		open, _ := parseFloat(item[1])
		high, _ := parseFloat(item[2])
		low, _ := parseFloat(item[3])
		close, _ := parseFloat(item[4])
		volume, _ := parseFloat(item[5])
		closeTime := int64(item[6].(float64))

		klines[i] = Kline{
			OpenTime:  openTime,
			Open:      open,
			High:      high,
			Low:      low,
			Close:     close,
			Volume:    volume,
			CloseTime: closeTime,
		}
	}

	return klines, nil
}

// getOpenInterestData 获取Open Interest数据
func getOpenInterestData(symbol string) (*OIData, error) {
	// 实现获取OI数据的逻辑
	return &OIData{Latest: 0, Average: 0}, nil
}

// getFundingRate 获取Funding Rate
func getFundingRate(symbol string) (float64, error) {
	// 实现获取Funding Rate的逻辑
	return 0.0, nil
}

// Format 格式化市场数据输出
func Format(data *Data) string {
	if data == nil {
		return "无市场数据"
	}

	var sb strings.Builder
	
	// 基础价格信息
	sb.WriteString(fmt.Sprintf("💰 当前价格: %.4f | 1h: %+.2f%% | 4h: %+.2f%% | 1d: %+.2f%%\n", 
		data.CurrentPrice, data.PriceChange1h, data.PriceChange4h, data.PriceChange1d))
	
	// 技术指标
	sb.WriteString(fmt.Sprintf("📊 EMA20: %.4f | MACD: %.4f | RSI7: %.1f\n", 
		data.CurrentEMA20, data.CurrentMACD, data.CurrentRSI7))
	
	// 多时间框架分析
	if data.MultiTimeframe != nil {
		sb.WriteString("⏰ 多时间框架:\n")
		
		// 15分钟框架
		if tf15 := data.MultiTimeframe.Timeframe15m; tf15 != nil {
			sb.WriteString(fmt.Sprintf("   • 15m: %s(强度%d) | EMA20:%.4f | MACD:%.4f | RSI:%.1f\n", 
				tf15.TrendDirection, tf15.SignalStrength, tf15.EMA20, tf15.MACD, tf15.RSI7))
		}
		
		// 1小时框架
		if tf1h := data.MultiTimeframe.Timeframe1h; tf1h != nil {
			sb.WriteString(fmt.Sprintf("   • 1h:  %s(强度%d) | EMA20:%.4f | MACD:%.4f | RSI:%.1f\n", 
				tf1h.TrendDirection, tf1h.SignalStrength, tf1h.EMA20, tf1h.MACD, tf1h.RSI7))
		}
		
		// 4小时框架
		if tf4h := data.MultiTimeframe.Timeframe4h; tf4h != nil {
			sb.WriteString(fmt.Sprintf("   • 4h:  %s(强度%d) | EMA20:%.4f | MACD:%.4f | RSI:%.1f\n", 
				tf4h.TrendDirection, tf4h.SignalStrength, tf4h.EMA20, tf4h.MACD, tf4h.RSI7))
		}
		
		// 日线框架
		if tf1d := data.MultiTimeframe.Timeframe1d; tf1d != nil {
			sb.WriteString(fmt.Sprintf("   • 1d:  %s(强度%d) | EMA20:%.4f | MACD:%.4f | RSI:%.1f\n", 
				tf1d.TrendDirection, tf1d.SignalStrength, tf1d.EMA20, tf1d.MACD, tf1d.RSI7))
		}
	}
	
	// 资金数据
	if data.OpenInterest != nil {
		sb.WriteString(fmt.Sprintf("📈 持仓量: %.0f | 平均: %.0f\n", 
			data.OpenInterest.Latest, data.OpenInterest.Average))
	}
	
	sb.WriteString(fmt.Sprintf("💸 资金费率: %.4f%%\n", data.FundingRate*100))
	
	// 长期数据
	if data.LongerTermContext != nil {
		sb.WriteString("📅 长期数据:\n")
		sb.WriteString(fmt.Sprintf("   • EMA20: %.4f | EMA50: %.4f\n", 
			data.LongerTermContext.EMA20, data.LongerTermContext.EMA50))
		sb.WriteString(fmt.Sprintf("   • ATR3: %.4f | ATR14: %.4f\n", 
			data.LongerTermContext.ATR3, data.LongerTermContext.ATR14))
		sb.WriteString(fmt.Sprintf("   • 成交量: %.0f | 平均: %.0f\n", 
			data.LongerTermContext.CurrentVolume, data.LongerTermContext.AverageVolume))
		
		// MACD序列
		if len(data.LongerTermContext.MACDValues) > 0 {
			sb.WriteString(fmt.Sprintf("   • MACD序列: %.4f → %.4f\n", 
				data.LongerTermContext.MACDValues[0], 
				data.LongerTermContext.MACDValues[len(data.LongerTermContext.MACDValues)-1]))
		}
		
		// RSI序列
		if len(data.LongerTermContext.RSI14Values) > 0 {
			sb.WriteString(fmt.Sprintf("   • RSI序列: %.1f → %.1f\n", 
				data.LongerTermContext.RSI14Values[0], 
				data.LongerTermContext.RSI14Values[len(data.LongerTermContext.RSI14Values)-1]))
		}
	}
	
	// 价格序列分析
	if data.MultiTimeframe != nil && data.MultiTimeframe.Timeframe15m != nil {
		priceSeries := data.MultiTimeframe.Timeframe15m.PriceSeries
		if len(priceSeries) >= 2 {
			recentChange := ((priceSeries[len(priceSeries)-1] - priceSeries[len(priceSeries)-2]) / priceSeries[len(priceSeries)-2]) * 100
			sb.WriteString(fmt.Sprintf("📈 最新变动: %+.2f%%\n", recentChange))
		}
	}

	// ==================== 新增：市场状态显示 ====================
	marketCondition := DetectMarketCondition(data)
	sb.WriteString(fmt.Sprintf("🌊 市场状态: %s (置信度: %d%%)\n", 
		marketCondition.Condition, marketCondition.Confidence))
	sb.WriteString(fmt.Sprintf("   • EMA斜率: %.4f%% | 价格通道: %.2f%% | ATR比率: %.2f%%\n", 
		marketCondition.EMASlope, marketCondition.PriceChannel, marketCondition.ATRRatio))
	
	// ==================== 新增：市场结构和斐波那契信息 ====================
	if data.MarketStructure != nil {
		sb.WriteString("🏗️ 市场结构:\n")
		sb.WriteString(fmt.Sprintf("   • 偏向: %s | 波段高点: %d | 波段低点: %d\n", 
			data.MarketStructure.CurrentBias, 
			len(data.MarketStructure.SwingHighs),
			len(data.MarketStructure.SwingLows)))
		
		if len(data.MarketStructure.SwingHighs) > 0 && len(data.MarketStructure.SwingLows) > 0 {
			sb.WriteString(fmt.Sprintf("   • 最近波段: %.4f → %.4f\n", 
				data.MarketStructure.SwingHighs[len(data.MarketStructure.SwingHighs)-1],
				data.MarketStructure.SwingLows[len(data.MarketStructure.SwingLows)-1]))
		}
	}
	
	if data.FibLevels != nil {
		sb.WriteString("📐 斐波那契水平:\n")
		sb.WriteString(fmt.Sprintf("   • 0.5中线: %.4f | 0.618: %.4f | 0.705: %.4f\n", 
			data.FibLevels.Level500, data.FibLevels.Level618, data.FibLevels.Level705))
		sb.WriteString(fmt.Sprintf("   • OTE区间: %.4f - %.4f\n", 
			data.FibLevels.Level618, data.FibLevels.Level705))
		
		// 显示当前价格相对于斐波那契水平的位置
		currentPrice := data.CurrentPrice
		if currentPrice >= data.FibLevels.Level705 && currentPrice <= data.FibLevels.Level618 {
			sb.WriteString("   🎯 **当前价格在OTE黄金区间内**\n")
		} else if currentPrice > data.FibLevels.Level500 {
			sb.WriteString("   🔴 当前价格在溢价区\n")
		} else {
			sb.WriteString("   🟢 当前价格在折扣区\n")
		}
	}
	
	// 震荡市警告
	if marketCondition.Condition == "ranging" && marketCondition.Confidence > 60 {
		sb.WriteString("🚨 **震荡市警告**: 避免开仓，耐心等待趋势突破！\n")
	}

	return sb.String()
}

// Normalize 标准化symbol,确保是USDT交易对
func Normalize(symbol string) string {
	symbol = strings.ToUpper(symbol)
	if strings.HasSuffix(symbol, "USDT") {
		return symbol
	}
	return symbol + "USDT"
}

// parseFloat 解析float值
func parseFloat(v interface{}) (float64, error) {
	switch val := v.(type) {
	case string:
		return strconv.ParseFloat(val, 64)
	case float64:
		return val, nil
	case int:
		return float64(val), nil
	case int64:
		return float64(val), nil
	default:
		return 0, fmt.Errorf("unsupported type: %T", v)
	}
}

// GetMarketDataForSymbols 批量获取多个币种的市场数据
func GetMarketDataForSymbols(symbols []string) map[string]*Data {
	result := make(map[string]*Data)
	
	for _, symbol := range symbols {
		data, err := Get(symbol)
		if err != nil {
			// 单个币种失败不影响整体
			continue
		}
		result[symbol] = data
	}
	
	return result
}

// GetTrendSummary 获取趋势摘要
func GetTrendSummary(data *Data) string {
	if data == nil || data.MultiTimeframe == nil {
		return "数据不足"
	}
	
	var bullishCount, bearishCount, neutralCount int
	
	// 统计各时间框架趋势
	timeframes := []*TimeframeData{
		data.MultiTimeframe.Timeframe15m,
		data.MultiTimeframe.Timeframe1h,
		data.MultiTimeframe.Timeframe4h,
		data.MultiTimeframe.Timeframe1d, // 新增：包含日线
	}
	
	for _, tf := range timeframes {
		if tf != nil {
			switch tf.TrendDirection {
			case "bullish":
				bullishCount++
			case "bearish":
				bearishCount++
			case "neutral":
				neutralCount++
			}
		}
	}
	
	// 判断总体趋势
	if bullishCount >= 2 {
		return "📈 多头趋势"
	} else if bearishCount >= 2 {
		return "📉 空头趋势"
	} else if neutralCount >= 2 {
		return "➡️ 震荡整理"
	} else {
		return "🔀 趋势不明"
	}
}

// GetSignalStrength 获取综合信号强度
func GetSignalStrength(data *Data) int {
	if data == nil || data.MultiTimeframe == nil {
		return 0
	}
	
	var totalStrength int
	var count int
	
	// 计算各时间框架信号强度的平均值
	timeframes := []*TimeframeData{
		data.MultiTimeframe.Timeframe15m,
		data.MultiTimeframe.Timeframe1h,
		data.MultiTimeframe.Timeframe4h,
		data.MultiTimeframe.Timeframe1d, // 新增：包含日线
	}
	
	for _, tf := range timeframes {
		if tf != nil {
			totalStrength += tf.SignalStrength
			count++
		}
	}
	
	if count > 0 {
		return totalStrength / count
	}
	return 0
}

// IsStrongSignal 判断是否为强信号
func IsStrongSignal(data *Data) bool {
	signalStrength := GetSignalStrength(data)
	trendSummary := GetTrendSummary(data)
	
	// 强信号标准：信号强度>70且趋势明确
	return signalStrength > 70 && (trendSummary == "📈 多头趋势" || trendSummary == "📉 空头趋势")
}

// GetRiskLevel 获取风险等级
func GetRiskLevel(data *Data) string {
	if data == nil {
		return "未知"
	}
	
	rsi := data.CurrentRSI7
	macd := data.CurrentMACD
	
	// 基于RSI和MACD判断风险
	if rsi > 80 || rsi < 20 {
		return "🔴 高风险"
	} else if (rsi > 70 && macd < 0) || (rsi < 30 && macd > 0) {
		return "🟡 中风险"
	} else {
		return "🟢 低风险"
	}
}

// GetTradingRecommendation 获取交易建议
func GetTradingRecommendation(data *Data) string {
	if data == nil {
		return "观望"
	}
	
	trend := GetTrendSummary(data)
	signalStrength := GetSignalStrength(data)
	riskLevel := GetRiskLevel(data)
	
	if signalStrength < 60 {
		return "观望"
	}
	
	switch trend {
	case "📈 多头趋势":
		if riskLevel == "🟢 低风险" {
			return "考虑做多"
		} else if riskLevel == "🟡 中风险" {
			return "谨慎做多"
		} else {
			return "观望"
		}
	case "📉 空头趋势":
		if riskLevel == "🟢 低风险" {
			return "考虑做空"
		} else if riskLevel == "🟡 中风险" {
			return "谨慎做空"
		} else {
			return "观望"
		}
	default:
		return "观望"
	}
}

// GetPriceTargets 获取价格目标
func GetPriceTargets(data *Data) (float64, float64) {
	if data == nil {
		return 0, 0
	}
	
	currentPrice := data.CurrentPrice
	atr := data.LongerTermContext.ATR14
	
	// 基于ATR计算止损和止盈
	stopLoss := currentPrice - (atr * 2)  // 2倍ATR止损
	takeProfit := currentPrice + (atr * 6) // 6倍ATR止盈（风险回报比1:3）
	
	return stopLoss, takeProfit
}

// ValidateForTrading 验证是否适合交易
func ValidateForTrading(data *Data) (bool, string) {
	if data == nil {
		return false, "数据无效"
	}
	
	// 检查持仓量
	if data.OpenInterest != nil && data.OpenInterest.Latest > 0 {
		oiValue := data.OpenInterest.Latest * data.CurrentPrice
		oiValueInMillions := oiValue / 1_000_000
		if oiValueInMillions < 15 {
			return false, fmt.Sprintf("持仓价值过低(%.2fM USD < 15M)", oiValueInMillions)
		}
	}
	
	// 检查信号强度
	if !IsStrongSignal(data) {
		return false, "信号强度不足"
	}
	
	// 检查风险等级
	riskLevel := GetRiskLevel(data)
	if riskLevel == "🔴 高风险" {
		return false, "风险等级过高"
	}
	
	// ==================== 新增：震荡市过滤 ====================
	marketCondition := DetectMarketCondition(data)
	if marketCondition.Condition == "ranging" && marketCondition.Confidence > 60 {
		return false, fmt.Sprintf("震荡市(置信度%d%%)，避免开仓", marketCondition.Confidence)
	}
	
	return true, "适合交易"
}

// GetMarketConditionSummary 获取市场状态摘要（新增函数）
func GetMarketConditionSummary(data *Data) string {
	if data == nil {
		return "数据不足"
	}
	
	condition := DetectMarketCondition(data)
	
	switch condition.Condition {
	case "trending":
		return fmt.Sprintf("📈 趋势市(置信度%d%%)", condition.Confidence)
	case "ranging":
		return fmt.Sprintf("🔄 震荡市(置信度%d%%)", condition.Confidence)
	case "volatile":
		return fmt.Sprintf("🌊 波动市(置信度%d%%)", condition.Confidence)
	default:
		return "🔍 状态不明"
	}
}

// ShouldAvoidTrading 是否应避免交易（新增函数）
func ShouldAvoidTrading(data *Data) (bool, string) {
	if data == nil {
		return true, "数据无效"
	}
	
	// 检查震荡市
	marketCondition := DetectMarketCondition(data)
	if marketCondition.Condition == "ranging" && marketCondition.Confidence > 60 {
		return true, fmt.Sprintf("高置信度震荡市(%d%%)，建议观望", marketCondition.Confidence)
	}
	
	// 检查其他不适合交易的条件
	if valid, reason := ValidateForTrading(data); !valid {
		return true, reason
	}
	
	return false, "适合交易"
}
