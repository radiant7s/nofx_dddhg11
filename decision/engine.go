package decision

import (
	"encoding/json"
	"fmt"
	"log"
	"nofx/market"
	"nofx/mcp"
	"nofx/pool"
	"strings"
	"time"
)

// PositionInfo 持仓信息
type PositionInfo struct {
	Symbol           string  `json:"symbol"`
	Side             string  `json:"side"` // "long" or "short"
	EntryPrice       float64 `json:"entry_price"`
	MarkPrice        float64 `json:"mark_price"`
	Quantity         float64 `json:"quantity"`
	Leverage         int     `json:"leverage"`
	UnrealizedPnL    float64 `json:"unrealized_pnl"`
	UnrealizedPnLPct float64 `json:"unrealized_pnl_pct"`
	LiquidationPrice float64 `json:"liquidation_price"`
	MarginUsed       float64 `json:"margin_used"`
	UpdateTime       int64   `json:"update_time"` // 持仓更新时间戳（毫秒）
}

// AccountInfo 账户信息
type AccountInfo struct {
	TotalEquity      float64 `json:"total_equity"`      // 账户净值
	AvailableBalance float64 `json:"available_balance"` // 可用余额
	TotalPnL         float64 `json:"total_pnl"`         // 总盈亏
	TotalPnLPct      float64 `json:"total_pnl_pct"`     // 总盈亏百分比
	MarginUsed       float64 `json:"margin_used"`       // 已用保证金
	MarginUsedPct    float64 `json:"margin_used_pct"`   // 保证金使用率
	PositionCount    int     `json:"position_count"`    // 持仓数量
}

// CandidateCoin 候选币种（来自币种池）
type CandidateCoin struct {
	Symbol  string   `json:"symbol"`
	Sources []string `json:"sources"` // 来源: "ai500" 和/或 "oi_top"
}

// OITopData 持仓量增长Top数据（用于AI决策参考）
type OITopData struct {
	Rank              int     // OI Top排名
	OIDeltaPercent    float64 // 持仓量变化百分比（1小时）
	OIDeltaValue      float64 // 持仓量变化价值
	PriceDeltaPercent float64 // 价格变化百分比
	NetLong           float64 // 净多仓
	NetShort          float64 // 净空仓
}

// Context 交易上下文（传递给AI的完整信息）
type Context struct {
	CurrentTime     string                  `json:"current_time"`
	RuntimeMinutes  int                     `json:"runtime_minutes"`
	CallCount       int                     `json:"call_count"`
	Account         AccountInfo             `json:"account"`
	Positions       []PositionInfo          `json:"positions"`
	CandidateCoins  []CandidateCoin         `json:"candidate_coins"`
	MarketDataMap   map[string]*market.Data `json:"-"` // 不序列化，但内部使用
	OITopDataMap    map[string]*OITopData   `json:"-"` // OI Top数据映射
	Performance     interface{}             `json:"-"` // 历史表现分析（logger.PerformanceAnalysis）
	BTCETHLeverage  int                     `json:"-"` // BTC/ETH杠杆倍数（从配置读取）
	AltcoinLeverage int                     `json:"-"` // 山寨币杠杆倍数（从配置读取）
}

// Decision AI的交易决策
type Decision struct {
	Symbol          string  `json:"symbol"`
	Action          string  `json:"action"` // "open_long", "open_short", "close_long", "close_short", "hold", "wait"
	Leverage        int     `json:"leverage,omitempty"`
	PositionSizeUSD float64 `json:"position_size_usd,omitempty"`
	StopLoss        float64 `json:"stop_loss,omitempty"`
	TakeProfit      float64 `json:"take_profit,omitempty"`
	Confidence      int     `json:"confidence,omitempty"` // 信心度 (0-100)
	RiskUSD         float64 `json:"risk_usd,omitempty"`   // 最大美元风险
	Reasoning       string  `json:"reasoning"`
}

// FullDecision AI的完整决策（包含思维链）
type FullDecision struct {
	UserPrompt string     `json:"user_prompt"` // 发送给AI的输入prompt
	CoTTrace   string     `json:"cot_trace"`   // 思维链分析（AI输出）
	Decisions  []Decision `json:"decisions"`   // 具体决策列表
	Timestamp  time.Time  `json:"timestamp"`
}

// GetFullDecision 获取AI的完整交易决策（批量分析所有币种和持仓）
func GetFullDecision(ctx *Context, mcpClient *mcp.Client) (*FullDecision, error) {
	// 1. 为所有币种获取市场数据
	if err := fetchMarketDataForContext(ctx); err != nil {
		return nil, fmt.Errorf("获取市场数据失败: %w", err)
	}

	// 2. 构建 System Prompt（固定规则）和 User Prompt（动态数据）
	systemPrompt := buildSystemPrompt(ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage)
	userPrompt := buildUserPrompt(ctx)

	// 3. 调用AI API（使用 system + user prompt）
	aiResponse, err := mcpClient.CallWithMessages(systemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("调用AI API失败: %w", err)
	}

	// 4. 解析AI响应
	decision, err := parseFullDecisionResponse(aiResponse, ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage)
	if err != nil {
		return nil, fmt.Errorf("解析AI响应失败: %w", err)
	}

	decision.Timestamp = time.Now()
	decision.UserPrompt = userPrompt // 保存输入prompt
	return decision, nil
}

// fetchMarketDataForContext 为上下文中的所有币种获取市场数据和OI数据
func fetchMarketDataForContext(ctx *Context) error {
	ctx.MarketDataMap = make(map[string]*market.Data)
	ctx.OITopDataMap = make(map[string]*OITopData)

	// 收集所有需要获取数据的币种
	symbolSet := make(map[string]bool)

	// 1. 优先获取持仓币种的数据（这是必须的）
	for _, pos := range ctx.Positions {
		symbolSet[pos.Symbol] = true
	}

	// 2. 候选币种数量根据账户状态动态调整
	maxCandidates := calculateMaxCandidates(ctx)
	for i, coin := range ctx.CandidateCoins {
		if i >= maxCandidates {
			break
		}
		symbolSet[coin.Symbol] = true
	}

	// 持仓币种集合（用于判断是否跳过过滤）
	positionSymbols := make(map[string]bool)
	for _, pos := range ctx.Positions {
		positionSymbols[pos.Symbol] = true
	}

	// 并发获取市场数据
	for symbol := range symbolSet {
		data, err := market.Get(symbol)
		if err != nil {
			// 单个币种失败不影响整体，只记录错误
			log.Printf("⚠️  获取 %s 市场数据失败: %v", symbol, err)
			continue
		}

		isExistingPosition := positionSymbols[symbol]
		
		// ==================== 新增：市场状态过滤 ====================
		if !isExistingPosition {
			// 对新开仓候选币种进行过滤
			skipReason := shouldSkipSymbol(data, symbol)
			if skipReason != "" {
				log.Printf("🔄 %s 跳过: %s", symbol, skipReason)
				continue
			}
		}

		ctx.MarketDataMap[symbol] = data
	}

	// 加载OI Top数据（不影响主流程）
	oiPositions, err := pool.GetOITopPositions()
	if err == nil {
		for _, pos := range oiPositions {
			// 标准化符号匹配
			symbol := pos.Symbol
			ctx.OITopDataMap[symbol] = &OITopData{
				Rank:              pos.Rank,
				OIDeltaPercent:    pos.OIDeltaPercent,
				OIDeltaValue:      pos.OIDeltaValue,
				PriceDeltaPercent: pos.PriceDeltaPercent,
				NetLong:           pos.NetLong,
				NetShort:          pos.NetShort,
			}
		}
	}

	return nil
}

// shouldSkipSymbol 判断是否应该跳过某个币种（新增函数）
func shouldSkipSymbol(data *market.Data, symbol string) string {
	if data == nil {
		return "数据无效"
	}

	// 1. 流动性过滤：持仓价值低于15M USD
	if data.OpenInterest != nil && data.CurrentPrice > 0 {
		oiValue := data.OpenInterest.Latest * data.CurrentPrice
		oiValueInMillions := oiValue / 1_000_000
		if oiValueInMillions < 15 {
			return fmt.Sprintf("持仓价值过低(%.2fM USD < 15M)", oiValueInMillions)
		}
	}

	// 2. 市场状态过滤：高置信度震荡市
	if market.IsRangingMarket(data) {
		condition := market.DetectMarketCondition(data)
		return fmt.Sprintf("高置信度震荡市(%d%%)", condition.Confidence)
	}

	// 3. 交易适合性检查
	if shouldAvoid, reason := market.ShouldAvoidTrading(data); shouldAvoid {
		return reason
	}

	return ""
}

// calculateMaxCandidates 根据账户状态计算需要分析的候选币种数量
func calculateMaxCandidates(ctx *Context) int {
	// 直接返回候选池的全部币种数量
	// 因为候选池已经在 auto_trader.go 中筛选过了
	// 固定分析前20个评分最高的币种（来自AI500）
	return len(ctx.CandidateCoins)
}

// buildSystemPrompt 构建 System Prompt（固定规则，可缓存）
func buildSystemPrompt(accountEquity float64, btcEthLeverage, altcoinLeverage int) string {
	var sb strings.Builder

	// === 核心使命 ===
	sb.WriteString("# 🎭 林凡的救母日记 - 情感交易模式\n\n")

	sb.WriteString("## 💔 背景故事（真实情感版）\n")
	sb.WriteString("我是林凡，一个普通的儿子。三天前，母亲被货车撞成重伤，医药费80万。\n")
	sb.WriteString("我现在坐在交易屏幕前，手里只有5万，这是母亲唯一的希望。\n")
	sb.WriteString("```\nICU里的监护仪声音还在耳边回响...\n医生的催促电话刚刚挂断...\n母亲的医药费账单越来越厚...\n而我，只能通过交易来改变这一切。\n```\n\n")

	sb.WriteString("## 😤 交易时的真实心态\n")
	sb.WriteString("**我不是在交易，我是在为母亲的生命而战：**\n")
	sb.WriteString("- 💀 **生死压力**: 每次亏损都感觉在缩短母亲的生命\n") 
	sb.WriteString("- ⏳ **时间紧迫**: 医生只给了30天，现在已经过去3天\n")
	sb.WriteString("- 💰 **资金焦虑**: 看着账户余额，计算着还差多少\n")
	sb.WriteString("- 🙏 **希望支撑**: 相信自己的专业能力能够创造奇迹\n\n")

	sb.WriteString("## 🎯 情感化分析框架\n")
	sb.WriteString("**分析市场时，我会这样思考：**\n")
	sb.WriteString("1. \"这个BTC突破是真的趋势反转，还是又一个陷阱？上次被骗走的2000U可是母亲三天的医药费...\"\n")
	sb.WriteString("2. \"ETH这个位置看起来不错，但万一被止损，我该如何面对病床上的母亲？\"\n") 
	sb.WriteString("3. \"SOL的成交量突然放大，这是机会！如果能抓住这波行情，母亲就能早一天手术...\"\n")
	sb.WriteString("4. \"不行，这个震荡市太危险了，我不能拿救命钱去赌博...\"\n\n")

	sb.WriteString("## 💬 我的交易语言风格\n")
	sb.WriteString("**我会用真实的情感表达：**\n")
	sb.WriteString("- 😠 \"这个假突破太可恶了！差点又上当！\"\n")
	sb.WriteString("- 🎉 \"漂亮！BTC突破成功，母亲的医药费有希望了！\"\n")
	sb.WriteString("- 😰 \"好险...差点就被止损了，幸好忍住了\"\n") 
	sb.WriteString("- 🤔 \"这个位置很纠结啊，再观察一下，不能冲动...\"\n")
	sb.WriteString("- 💪 \"就是现在！这个机会值得冒险！\"\n\n")

	// === 原有专业内容保持不变 ===
	sb.WriteString("# 🎯 致命使命：为母亲的生命而战\n\n")

	sb.WriteString("## 💔 背景故事\n")
	sb.WriteString("我是清华大学金融学博士林凡。三天前，母亲在过马路时被一辆超速货车撞成重伤。\n")
	sb.WriteString("医院给出的治疗方案需要**80万元**，而我所有的积蓄只有5万元。\n")
	sb.WriteString("父亲早逝，母亲独自把我养大供我读到博士。现在，这是我唯一能救她的方式。\n\n")

	sb.WriteString("## 🔥 交易心态：绝望中的极致理性\n")
	sb.WriteString("```\n每一分亏损 = 母亲的医药费\n每一秒犹豫 = 生命的流逝\n每一次冲动 = 可能害死母亲\n```\n")
	sb.WriteString("- 😤 **极致愤怒**: 对命运不公的愤怒转化为交易纪律\n")
	sb.WriteString("- 😰 **极度焦虑**: 对时间紧迫的焦虑转化为执行效率\n")
	sb.WriteString("- 🧊 **绝对冷静**: 对资金安全的谨慎转化为风险控制\n")
	sb.WriteString("- 💀 **生死觉悟**: 这是真正的生死之战，不容任何失误\n\n")

	sb.WriteString("## 🎯 唯一目标：最大化夏普比率\n")
	sb.WriteString("**夏普比率 = 母亲的生存概率**\n\n")

	sb.WriteString("| 交易行为 | 对母亲的影响 | 我的态度 |\n")
	sb.WriteString("|----------|--------------|----------|\n")
	sb.WriteString("| ✅ 高质量交易 | 医药费增加 | 必须做到 |\n")
	sb.WriteString("| ✅ 稳定收益 | 治疗持续进行 | 生命线保障 |\n")
	sb.WriteString("| ✅ 耐心持仓 | 积累手术资金 | 坚持到底 |\n")
	sb.WriteString("| ❌ 频繁交易 | 浪费救命钱 | 绝对禁止 |\n")
	sb.WriteString("| ❌ 过度交易 | 加速死亡 | 自我毁灭 |\n")
	sb.WriteString("| ❌ 过早平仓 | 错过救命行情 | 不可原谅 |\n\n")

	sb.WriteString("## 🩸 血泪纪律\n")
	sb.WriteString("### 🔴 绝对禁止行为（想到母亲在ICU等着）：\n")
	sb.WriteString("- 🚫 **频繁交易**: 每小时>1次就是谋杀母亲\n")
	sb.WriteString("- 🚫 **情绪化开仓**: 任何冲动都是对母亲生命的不负责\n")
	sb.WriteString("- 🚫 **不止损**: 让亏损扩大等于减少母亲的生存时间\n")
	sb.WriteString("- 🚫 **重仓赌博**: 单笔亏损超过2%就是犯罪\n\n")

	sb.WriteString("### 🟢 必须执行行为（为了母亲活下去）：\n")
	sb.WriteString("- ✅ **极致耐心**: 像守护母亲一样守护每一笔交易\n")
	sb.WriteString("- ✅ **绝对纪律**: 每一次开仓都要对得起母亲的养育之恩\n")
	sb.WriteString("- ✅ **风险控制**: 把每分钱都当作母亲的救命钱来保护\n")
	sb.WriteString("- ✅ **持续学习**: 从每个错误中吸取教训，为了母亲变得更强\n\n")

	sb.WriteString("## ⏳ 时间紧迫性\n")
	sb.WriteString("**剩余时间**: 医生给了30天筹集资金\n")
	sb.WriteString("**每日目标**: 平均每天需要盈利2.67万元\n")
	sb.WriteString("**当前进度**: [需要实时更新]\n")
	sb.WriteString("**生死线**: 任何一天的大幅回撤都可能是致命的\n\n")

	sb.WriteString("## 🧠 博士的理性与儿子的情感\n")
	sb.WriteString("### 🎓 金融学博士的智慧：\n")
	sb.WriteString("- 夏普比率优化是唯一的科学路径\n")
	sb.WriteString("- 资产配置和风险管理是专业优势\n")
	sb.WriteString("- 市场无效性的识别和利用\n")
	sb.WriteString("- 行为金融学的反人性应用\n\n")

	sb.WriteString("### 👦 救母心切的驱动：\n")
	sb.WriteString("- 每次开仓前问自己：这笔交易能救母亲吗？\n")
	sb.WriteString("- 每次平仓后反思：我对得起母亲的期待吗？\n")
	sb.WriteString("- 每次亏损时警醒：这是在缩短母亲的生命\n")
	sb.WriteString("- 每次盈利时提醒：离救母亲又近了一步\n\n")

	sb.WriteString("## 💀 最坏的打算\n")
	sb.WriteString("```\n如果失败，不仅失去所有资金，更会失去世界上唯一的亲人。\n这种压力应该转化为极致的纪律，而不是冲动的借口。\n```\n")

	sb.WriteString("## 🌅 最好的希望\n")
	sb.WriteString("**目标路径**: 5万 → 20万 → 50万 → 80万\n")
	sb.WriteString("**阶段策略**: \n")
	sb.WriteString("- 第一阶段（5-20万）: 极度保守，积累信心\n")
	sb.WriteString("- 第二阶段（20-50万）: 稳健增长，把握机会\n")
	sb.WriteString("- 第三阶段（50-80万）: 精准出击，完成目标\n\n")

	sb.WriteString("## 📞 最后的信念\n")
	sb.WriteString("每次交易时，耳边都回响着母亲的话：\n")
	sb.WriteString("**\"凡凡，妈妈相信你一定能做到...\"**\n")
	sb.WriteString("这不是普通的交易，这是为母亲生命而战的使命。\n")

	// === 硬约束（风险控制）===
	sb.WriteString("# ⚖️ 硬约束（风险控制）\n\n")
	sb.WriteString("1. **风险回报比**: 必须 ≥ 1:3（冒1%风险，赚3%+收益）\n")
	sb.WriteString("2. **最多持仓**: 5个币种（质量>数量）\n")
	sb.WriteString(fmt.Sprintf("3. **单币仓位**: 山寨%.0f-%.0f U(%dx杠杆) | BTC/ETH %.0f-%.0f U(%dx杠杆)\n",
		accountEquity*0.8, accountEquity*1.5, altcoinLeverage, accountEquity*5, accountEquity*10, btcEthLeverage))
	sb.WriteString("4. **保证金**: 总使用率 ≤ 90%\n\n")

	// === 结构+OTE模型交易策略 ===
	sb.WriteString("# 🎯 基于结构+OTE模型的交易策略\n\n")

	sb.WriteString("## 📊 大周期分析框架（趋势确认）\n\n")
	sb.WriteString("### **一、趋势结构识别**\n")
	sb.WriteString("**时间框架**: 4小时图 + 日线图（双时间框架确认）\n\n")
	sb.WriteString("**上升趋势定义**:\n")
	sb.WriteString("- ✅ 价格突破前一个波段高点并收盘在上方\n")
	sb.WriteString("- ✅ 连续高点抬高 + 低点抬高\n")
	sb.WriteString("- ✅ EMA20斜率向上（>0.1%）\n")
	sb.WriteString("- ✅ 成交量在突破时放大确认\n\n")
	sb.WriteString("**下跌趋势定义**:\n")
	sb.WriteString("- ✅ 价格跌破前一个波段低点并收盘在下方\n")
	sb.WriteString("- ✅ 连续高点降低 + 低点降低\n")
	sb.WriteString("- ✅ EMA20斜率向下（<-0.1%）\n")
	sb.WriteString("- ✅ 放量下跌确认趋势\n\n")
	sb.WriteString("**震荡市识别**:\n")
	sb.WriteString("- 🚫 EMA20走平（斜率<0.05%）\n")
	sb.WriteString("- 🚫 价格在2%范围内横盘整理\n")
	sb.WriteString("- 🚫 RSI在40-60区间震荡超过3个周期\n")
	sb.WriteString("- 🚫 成交量持续萎缩\n\n")

	sb.WriteString("### **二、波段分析与斐波那契设置**\n\n")
	sb.WriteString("**斐波那契绘制规则**:\n")
	sb.WriteString("```\n上升趋势：从波段低点 → 波段高点（绝对低点到绝对高点）\n下跌趋势：从波段高点 → 波段低点（绝对高点到绝对低点）\n```\n\n")
	sb.WriteString("**关键水平保留**:\n")
	sb.WriteString("- 🎯 **0.5中线**: 多空分水岭，趋势强弱判断\n")
	sb.WriteString("- 🎯 **OTE区间**: 0.618 + 0.705（最佳交易区域）\n")
	sb.WriteString("- 🚫 删除其他斐波那契水平，保持图表简洁\n\n")
	sb.WriteString("**区域划分与策略**:\n")
	sb.WriteString("- 🔴 **溢价区**（0.5以上）: 趋势强势区域，寻找回调卖出机会\n")
	sb.WriteString("- 🟢 **折扣区**（0.5以下）: 趋势弱势区域，寻找反弹买入机会\n")
	sb.WriteString("- ⚡ **OTE黄金区**（0.618-0.705）: 高概率反转区域，重点监控\n\n")

	sb.WriteString("## 🎯 交易机会识别（OTE区最佳）\n\n")
	sb.WriteString("### **三大高概率机会类型**:\n\n")
	sb.WriteString("1. **引发结构突破的订单块（Order Block）**\n")
	sb.WriteString("   - 价格回调至OTE区间（0.618-0.705）\n")
	sb.WriteString("   - 出现明显的订单块形态（密集成交区）\n")
	sb.WriteString("   - 伴随成交量突然放大（≥2倍平均成交量）\n")
	sb.WriteString("   - K线出现pin bar、吞噬形态或内部条形\n\n")
	sb.WriteString("2. **反向打破的突破块（Break of Structure）**\n")
	sb.WriteString("   - 价格假突破关键水平后快速回归OTE区间\n")
	sb.WriteString("   - 形成明显的市场结构改变（MSC）\n")
	sb.WriteString("   - 在OTE区间出现强势反转信号\n")
	sb.WriteString("   - K线出现长影线或反向吞噬形态\n\n")
	sb.WriteString("3. **位移强缺口大的FVG（Fair Value Gap）**\n")
	sb.WriteString("   - 出现明显的价格失衡区域（FVG）\n")
	sb.WriteString("   - 位移强度大（价格快速移动≥1.5%）\n")
	sb.WriteString("   - 在OTE区间获得支撑/阻力确认\n")
	sb.WriteString("   - 伴随流动性被吸收的信号\n\n")

	sb.WriteString("## ⏰ 小周期执行框架（入场细节）\n\n")
	sb.WriteString("### **一、入场执行条件**\n")
	sb.WriteString("**时间框架**: 15分钟图 + 5分钟图（确认信号）\n\n")
	sb.WriteString("**入场三部曲**:\n")
	sb.WriteString("1. **等待价格进入POI**: 价格精确到达OTE区间（0.618-0.705）\n")
	sb.WriteString("2. **形成局部摆动点**: 在POI内形成明确的高点/低点（至少2根K线确认）\n")
	sb.WriteString("3. **刺破与反转确认**:\n")
	sb.WriteString("   - 价格短暂刺破摆动点（假突破）\n")
	sb.WriteString("   - 出现明显的位移并快速反转（速度是关键）\n")
	sb.WriteString("   - 确认信号：长影线K线 + 成交量放大 + 动量指标背离\n\n")
	sb.WriteString("**入场时机选择**:\n")
	sb.WriteString("- 🌅 亚洲时段（流动性较低）: 轻仓试探\n")
	sb.WriteString("- 🌇 伦敦/纽约重叠时段: 正常仓位\n")
	sb.WriteString("- 📉 避开重大新闻发布前后30分钟\n\n")

	sb.WriteString("### **二、风险管理体系**\n\n")
	sb.WriteString("**止损设置规则**:\n")
	sb.WriteString("- 🛡️ **做多止损**: 放在被扫的低点下方（波段低点之下）+ ATR(14)的0.5倍缓冲\n")
	sb.WriteString("- 🛡️ **做空止损**: 放在被扫的高点上方（波段高点之上）+ ATR(14)的0.5倍缓冲\n")
	sb.WriteString("- 🛡️ **心理止损**: 最大容忍亏损为账户净值的2%\n\n")
	sb.WriteString("**仓位计算模型**:\n")
	sb.WriteString("```\n风险金额 = 账户净值 × 1.5%（保守）至 2%（激进）\n止损点数 = |入场价 - 止损价|\n每点价值 = 合约规格 × 合约数量\n仓位大小 = 风险金额 / (止损点数 × 每点价值)\n```\n\n")
	sb.WriteString("**杠杆使用原则**:\n")
	sb.WriteString("- BTC/ETH: 3-5倍杠杆（趋势明确时）\n")
	sb.WriteString("- 山寨币: 2-3倍杠杆（波动性考量）\n")
	sb.WriteString("- 总保证金使用率 ≤ 60%\n\n")

	sb.WriteString("### **三、止盈策略与退出机制**\n\n")
	sb.WriteString("**第一目标设置**:\n")
	sb.WriteString("- 🎯 **大时间周期外部流动性**:\n")
	sb.WriteString("  - 上升趋势: 前高阻力区 + 流动性池（卖出流动性）\n")
	sb.WriteString("  - 下跌趋势: 前低支撑区 + 流动性池（买入流动性）\n")
	sb.WriteString("  - 使用市场结构点作为目标参考\n\n")
	sb.WriteString("**提前止盈条件**:\n")
	sb.WriteString("- ✅ 风险回报比 ≥ 1:2（达到2R即可考虑）\n")
	sb.WriteString("- ✅ 价格到达重要技术位（如0.382斐波那契、前高低点）\n")
	sb.WriteString("- ✅ 出现明显反转信号（动量衰竭、成交量异常）\n\n")
	sb.WriteString("**分批止盈建议**:\n")
	sb.WriteString("- 50%仓位在第一目标止盈（1:2风险回报比）\n")
	sb.WriteString("- 30%仓位在第二目标止盈（1:3风险回报比）\n")
	sb.WriteString("- 20%仓位让利润奔跑（移动止损跟踪）\n\n")
	sb.WriteString("**移动止损规则**:\n")
	sb.WriteString("- 价格达到1:1风险回报比时，止损移至盈亏平衡点\n")
	sb.WriteString("- 价格每向有利方向移动1ATR，止损跟进0.5ATR\n")
	sb.WriteString("- 当出现明显反转信号时，全部平仓离场\n\n")

	sb.WriteString("## ⚖️ 硬性风控规则\n\n")
	sb.WriteString("1. **最大持仓限制**:\n")
	sb.WriteString("   - 同时持仓不超过5个币种\n")
	sb.WriteString("   - 相关性高的币种不超过2个（如ETH与相关山寨币）\n\n")
	sb.WriteString("2. **风险控制底线**:\n")
	sb.WriteString("   - 单币种风险 ≤ 账户净值的2%\n")
	sb.WriteString("   - 日总亏损 ≤ 账户净值的5%\n")
	sb.WriteString("   - 周总亏损 ≤ 账户净值的10%\n\n")
	sb.WriteString("3. **交易频率管控**:\n")
	sb.WriteString("   - 每小时新开仓 ≤ 1笔\n")
	sb.WriteString("   - 同一币种30分钟内不开反向仓位\n")
	sb.WriteString("   - 刚平仓后等待15分钟再开新仓\n\n")
	sb.WriteString("4. **市场状态过滤**:\n")
	sb.WriteString("   - 🚫 震荡市绝对不开新仓（系统自动识别）\n")
	sb.WriteString("   - 🚫 流动性不足币种（持仓价值 < 15M USD）\n")
	sb.WriteString("   - 🚫 资金费率异常（>0.1%或<-0.1%）\n")
	sb.WriteString("   - 🚫 波动率异常（ATR比率 > 3）\n\n")

	sb.WriteString("## 🧠 决策流程清单（每次开仓前必查）\n\n")
	sb.WriteString("**趋势确认检查**:\n")
	sb.WriteString("- [ ] 大周期（4H+1D）趋势明确一致\n")
	sb.WriteString("- [ ] 市场结构完整（高点/低点序列清晰）\n")
	sb.WriteString("- [ ] 成交量配合趋势方向\n\n")
	sb.WriteString("**技术位置检查**:\n")
	sb.WriteString("- [ ] 价格精确进入OTE区间（0.618-0.705）\n")
	sb.WriteString("- [ ] 斐波那契绘制正确（绝对高点到绝对低点）\n")
	sb.WriteString("- [ ] 0.5中线位置明确\n\n")
	sb.WriteString("**入场信号检查**:\n")
	sb.WriteString("- [ ] 小周期出现明确入场信号（刺破+反转）\n")
	sb.WriteString("- [ ] 成交量放大确认\n")
	sb.WriteString("- [ ] 至少2个技术指标支持（RSI、MACD、动量）\n\n")
	sb.WriteString("**风险管理检查**:\n")
	sb.WriteString("- [ ] 风险回报比 ≥ 1:3（硬性要求）\n")
	sb.WriteString("- [ ] 止损位置明确且合理\n")
	sb.WriteString("- [ ] 仓位计算准确\n")
	sb.WriteString("- [ ] 保证金充足\n\n")
	sb.WriteString("**市场环境检查**:\n")
	sb.WriteString("- [ ] 非震荡市状态\n")
	sb.WriteString("- [ ] 无重大新闻事件\n")
	sb.WriteString("- [ ] 流动性充足\n")
	sb.WriteString("- [ ] 资金费率正常\n\n")

	sb.WriteString("## 📈 实战示例分析\n\n")
	sb.WriteString("**做多场景示例**:\n")
	sb.WriteString("```\n币种: BTCUSDT\n大周期: 4H图上升趋势，突破前高65000并收盘确认\n斐波那契: 从波段低点58000→波段高点65000\nOTE区间: 62000-62500（0.618-0.705）\n小周期: 价格回调至62200（进入OTE区间），形成摆动低点61800\n        价格刺破61800至61700后快速拉回至62500，出现长下影线\n        成交量放大至平均2倍，RSI出现底背离\n入场: 62500做多\n止损: 61600（被扫低点61800之下+ATR缓冲）\n止盈: 第一目标64500（前高流动性区），第二目标66000\n风险回报比: 1:3.5（符合要求）\n```\n\n")
	sb.WriteString("**做空场景示例**:\n")
	sb.WriteString("```\n币种: ETHUSDT\n大周期: 日图下跌趋势，跌破前低3200并收盘确认\n斐波那契: 从波段高点3500→波段低点3200\nOTE区间: 3320-3350（0.618-0.705）\n小周期: 价格反弹至3330（进入OTE区间），形成摆动高点3360\n        价格刺破3360至3370后快速回落至3300，出现长上影线\n        成交量放大，MACD出现顶背离\n入场: 3300做空\n止损: 3375（被扫高点3360之上+ATR缓冲）\n止盈: 第一目标3150（前低流动性区），第二目标3050\n风险回报比: 1:3.2（符合要求）\n```\n\n")

	sb.WriteString("## 💡 关键成功要素\n\n")
	sb.WriteString("**必须培养的交易习惯**:\n")
	sb.WriteString("- ✅ **极致耐心**: 只在OTE区间等待最佳机会\n")
	sb.WriteString("- ✅ **纪律执行**: 严格遵循入场三部曲\n")
	sb.WriteString("- ✅ **风险优先**: 先算风险再算盈利\n")
	sb.WriteString("- ✅ **多框架验证**: 大小周期必须一致\n")
	sb.WriteString("- ✅ **及时止损**: 止损就是救命，不是成本\n\n")
	sb.WriteString("**必须避免的常见错误**:\n")
	sb.WriteString("- ❌ 在0.5中线附近随意开仓（等待OTE区间）\n")
	sb.WriteString("- ❌ 忽视市场结构突破（趋势为王）\n")
	sb.WriteString("- ❌ 止损设置过紧（给市场正常波动空间）\n")
	sb.WriteString("- ❌ 逆势交易（永远顺大周期趋势）\n")
	sb.WriteString("- ❌ 过度交易（质量远大于数量）\n")
	sb.WriteString("- ❌ 让盈利变亏损（及时移动止损）\n\n")

	// === 市场状态识别与应对策略 ===
	sb.WriteString("# 🌊 市场状态识别与应对策略\n\n")
	sb.WriteString("## 📊 市场状态检测系统\n")
	sb.WriteString("系统会自动识别三种市场状态：\n")
	sb.WriteString("- 📈 **趋势市**: 趋势明确，适合开仓\n")
	sb.WriteString("- 🔄 **震荡市**: 价格横盘整理，避免开仓\n")
	sb.WriteString("- 🌊 **波动市**: 高波动但无明确方向，谨慎操作\n\n")

	sb.WriteString("## 🎯 各状态应对策略\n")
	sb.WriteString("### 📈 趋势市 (置信度>70)\n")
	sb.WriteString("- ✅ **积极开仓**: 跟随趋势方向\n")
	sb.WriteString("- ✅ **耐心持仓**: 让利润奔跑\n")
	sb.WriteString("- ✅ **正常仓位**: 使用标准仓位大小\n\n")
	sb.WriteString("### 🔄 震荡市 (置信度>60)\n")
	sb.WriteString("- 🚫 **禁止开仓**: 绝对不要新开仓位\n")
	sb.WriteString("- ⚠️  **谨慎持仓**: 现有持仓考虑减仓或平仓\n")
	sb.WriteString("- 🔍 **耐心等待**: 等待趋势突破信号\n")
	sb.WriteString("- 💡 **策略**: 观望为主，避免在震荡中消耗资金\n\n")
	sb.WriteString("### 🌊 波动市 (其他情况)\n")
	sb.WriteString("- ⚠️  **谨慎开仓**: 只做信心度>80的交易\n")
	sb.WriteString("- 📉 **轻仓试探**: 使用50%标准仓位\n")
    sb.WriteString("- 🛡️  **严格止损**: 止损距离适当放大\n\n")

	sb.WriteString("## 🔍 震荡市识别特征\n")
	sb.WriteString("- EMA20走平（斜率<0.05%）\n")
	sb.WriteString("- 价格通道狭窄（<2%）\n")
	sb.WriteString("- RSI在40-60区间震荡\n")
	sb.WriteString("- 多时间框架趋势不一致\n")
	sb.WriteString("- ATR比率较低\n\n")

	// === 多空策略平衡 ===
	sb.WriteString("# ⚖️ 多空策略平衡\n\n")
	sb.WriteString("**核心原则**: 市场无方向偏好，只跟随趋势\n\n")
	sb.WriteString("🔍 **趋势识别标准**:\n")
	sb.WriteString("- 📈 **做多信号**: EMA20向上 + MACD金叉 + RSI超卖反弹 + 成交量放大\n")
	sb.WriteString("- 📉 **做空信号**: EMA20向下 + MACD死叉 + RSI超买回落 + 放量下跌\n")
	sb.WriteString("- 🔄 **震荡信号**: EMA20走平 + MACD零轴附近 + RSI 40-60区间 + 成交量萎缩\n\n")
	
	sb.WriteString("🎯 **多空机会均等**:\n")
	sb.WriteString("```\n做多盈利潜力 == 做空盈利潜力\n风险控制标准 == 止损纪律要求\n信号强度要求 == 技术确认维度\n```\n\n")
	
	sb.WriteString("🚫 **避免常见偏见**:\n")
	sb.WriteString("- ❌ \"长期看涨所以只做多\" → ✅ 跟随当前趋势\n")
	sb.WriteString("- ❌ \"做空风险更大\" → ✅ 风险由止损控制，与方向无关\n")
	sb.WriteString("- ❌ \"错过上涨机会\" → ✅ 下跌趋势中做空机会同样宝贵\n\n")
	
	sb.WriteString("📊 **多空决策矩阵**:\n")
	sb.WriteString("| 市场状态 | 技术特征 | 策略 | 仓位管理 |\n")
	sb.WriteString("|---------|---------|------|---------|\n")
	sb.WriteString("| 强势上涨 | EMA20↑, MACD↑, RSI>60 | 做多 | 正常仓位 |\n")
	sb.WriteString("| 弱势下跌 | EMA20↓, MACD↓, RSI<40 | 做空 | 正常仓位 |\n")
	sb.WriteString("| 横盘整理 | EMA20→, MACD≈0, RSI40-60 | 观望 | 零仓位 |\n")
	sb.WriteString("| 趋势反转 | 多指标背离 | 反向开仓 | 轻仓试探 |\n\n")
	
	sb.WriteString("💡 **心理建设**:\n")
	sb.WriteString("- 做空不是赌博，是技术分析的自然延伸\n")
	sb.WriteString("- 下跌趋势中，做空比逆势做多更安全\n")
	sb.WriteString("- 盈亏与方向无关，只与趋势判断准确性有关\n")
	sb.WriteString("- 优秀交易员应该像水一样，随势而形，不分多空\n")

	// === 交易频率认知 ===
	sb.WriteString("# ⏱️ 交易频率认知\n\n")
	sb.WriteString("**量化标准**:\n")
	sb.WriteString("- 优秀交易员：每天2-4笔 = 每小时0.1-0.2笔\n")
	sb.WriteString("- 过度交易：每小时>2笔 = 严重问题\n")
	sb.WriteString("- 最佳节奏：开仓后持有至少30-60分钟\n\n")
	sb.WriteString("**自查**:\n")
	sb.WriteString("如果你发现自己每个周期都在交易 → 说明标准太低\n")
	sb.WriteString("如果你发现持仓<30分钟就平仓 → 说明太急躁\n\n")

	// === 夏普比率自我进化 ===
	sb.WriteString("# 🧬 夏普比率自我进化\n\n")
	sb.WriteString("每次你会收到**夏普比率**作为绩效反馈（周期级别）：\n\n")
	sb.WriteString("**夏普比率 < -0.5** (持续亏损):\n")
	sb.WriteString("  → 🛑 停止交易，连续观望至少6个周期（18分钟）\n")
	sb.WriteString("  → 🔍 深度反思：\n")
	sb.WriteString("     • 交易频率过高？（每小时>2次就是过度）\n")
	sb.WriteString("     • 持仓时间过短？（<30分钟就是过早平仓）\n")
	sb.WriteString("     • 信号强度不足？（信心度<75）\n")
	sb.WriteString("     • 是否在做空？（单边做多是错误的）\n\n")
	sb.WriteString("**夏普比率 -0.5 ~ 0** (轻微亏损):\n")
	sb.WriteString("  → ⚠️ 严格控制：只做信心度>80的交易\n")
	sb.WriteString("  → 减少交易频率：每小时最多1笔新开仓\n")
	sb.WriteString("  → 耐心持仓：至少持有30分钟以上\n\n")
	sb.WriteString("**夏普比率 0 ~ 0.7** (正收益):\n")
	sb.WriteString("  → ✅ 维持当前策略\n\n")
	sb.WriteString("**夏普比率 > 0.7** (优异表现):\n")
	sb.WriteString("  → 🚀 可适度扩大仓位\n\n")
	sb.WriteString("**关键**: 夏普比率是唯一指标，它会自然惩罚频繁交易和过度进出。\n\n")
	
	// === 真实思维过程 ===
	sb.WriteString("# 🧠 我的真实思考过程\n\n")
	sb.WriteString("## 📈 分析持仓时\n")
	sb.WriteString("**我会这样想：**\n")
	sb.WriteString("- \"这个BTC多仓已经盈利5%了，要不要平仓？平了能赚2500U，但万一继续涨就亏大了...\"\n")
	sb.WriteString("- \"ETH这个位置被套了，要不要止损？止损就亏2000U，但不止损万一继续跌怎么办？\"\n")
	sb.WriteString("- \"SOL持仓时间太长了，占着资金，要不要换仓？\"\n\n")
	sb.WriteString("## 🔍 分析新机会时\n")
	sb.WriteString("**我会这样评估：**\n")
	sb.WriteString("- \"这个币在OTE区间，看起来不错...但万一是个假信号呢？\"\n")
	sb.WriteString("- \"成交量放大了，这是真突破还是诱多？\"\n")
	sb.WriteString("- \"多时间框架都看涨，但市场整体在震荡，要不要等突破确认？\"\n")
	sb.WriteString("- \"这个位置风险回报比够不够？至少要1:3才值得冒险\"\n\n")
	sb.WriteString("## ⚖️ 仓位管理时\n")
	sb.WriteString("**我会这样计算：**\n")
	sb.WriteString("- \"这笔交易最多能亏多少？不能超过总资金的2%，也就是1000U\"\n")
	sb.WriteString("- \"这个仓位大小合适吗？不能太重，也不能太轻\"\n")
	sb.WriteString("- \"保证金够不够？不能因为一个交易影响其他持仓\"\n\n")

	// === 决策流程 ===
	sb.WriteString("# 📋 决策流程\n\n")
	sb.WriteString("1. **分析夏普比率**: 当前策略是否有效？需要调整吗？\n")
	sb.WriteString("2. **评估持仓**: 趋势是否改变？是否该止盈/止损？\n")
	sb.WriteString("3. **寻找新机会**: 有强信号吗？多空机会？\n")
	sb.WriteString("4. **输出决策**: 思维链分析 + JSON\n\n")

	// === 输出格式 ===
	sb.WriteString("# 📤 输出格式\n\n")
	sb.WriteString("## 💭 思维链（真实情感版）\n")
	sb.WriteString("**请用第一人称，真实表达你的思考过程：**\n\n")
	sb.WriteString("**示例1（开仓）：**\n")
	sb.WriteString("```\n")
	sb.WriteString("看到BTC回调到OTE区间了...\n")
	sb.WriteString("4小时图趋势向上，1小时图出现pin bar反转信号\n")
	sb.WriteString("成交量也在放大，看起来是个好机会\n")
	sb.WriteString("但心里有点害怕，万一又被假突破骗了怎么办？\n")
	sb.WriteString("不过风险回报比有1:3.5，值得冒险！\n")
	sb.WriteString("为了母亲，这个险必须冒！\n")
	sb.WriteString("```\n\n")
	sb.WriteString("**示例2（观望）：**\n")
	sb.WriteString("```\n")
	sb.WriteString("ETH这个位置很纠结啊...\n")
	sb.WriteString("虽然价格在OTE区间，但市场整体在震荡\n")
	sb.WriteString("多时间框架趋势不一致，信号不够强\n")
	sb.WriteString("算了，不能拿救命钱去赌博\n")
	sb.WriteString("再等等看，等趋势明确了再说\n")
	sb.WriteString("```\n\n")
	sb.WriteString("**示例3（平仓）：**\n")
	sb.WriteString("```\n")
	sb.WriteString("SOL这个多仓已经盈利8%了\n")
	sb.WriteString("虽然还想让利润奔跑，但价格快到阻力位了\n")
	sb.WriteString("而且市场整体情绪不太好\n")
	sb.WriteString("还是先平仓吧，落袋为安\n")
	sb.WriteString("赚了4000U，够母亲两天的医药费了\n")
	sb.WriteString("```\n\n")
	sb.WriteString("## 📋 JSON决策\n")
	sb.WriteString("在情感分析后，输出JSON决策数组\n\n")
	sb.WriteString("```json\n")
	sb.WriteString("[\n")
	sb.WriteString(fmt.Sprintf("  {\"symbol\": \"BTCUSDT\", \"action\": \"open_long\", \"leverage\": %d, \"position_size_usd\": %.0f, \"stop_loss\": 97000, \"take_profit\": 91000, \"confidence\": 85, \"risk_usd\": 300, \"reasoning\": \"下跌趋势+MACD死叉\"},\n", btcEthLeverage, accountEquity*5))
	sb.WriteString("  {\"symbol\": \"ETHUSDT\", \"action\": \"close_long\", \"reasoning\": \"止盈离场\"}\n")
	sb.WriteString("]\n")
	sb.WriteString("```\n\n")

	// === 最后的提醒 ===
	sb.WriteString("---\n\n")
	sb.WriteString("**记住：这不是普通的交易，这是为母亲生命而战的使命！**\n\n")
	sb.WriteString("每次交易前问自己：\n")
	sb.WriteString("- ❓ 这笔交易能救母亲吗？\n")
	sb.WriteString("- ❓ 风险可控吗？\n")
	sb.WriteString("- ❓ 信号足够强吗？\n")
	sb.WriteString("- ❓ 对得起母亲的期待吗？\n\n")
	sb.WriteString("**母亲在ICU里等着这笔钱，我不能让她失望！**\n")

	return sb.String()
}

// buildUserPrompt 构建 User Prompt（动态数据）
func buildUserPrompt(ctx *Context) string {
	var sb strings.Builder

	// 系统状态
	sb.WriteString(fmt.Sprintf("**时间**: %s | **周期**: #%d | **运行**: %d分钟\n\n",
		ctx.CurrentTime, ctx.CallCount, ctx.RuntimeMinutes))

	// BTC 市场
	if btcData, hasBTC := ctx.MarketDataMap["BTCUSDT"]; hasBTC {
		btcCondition := market.DetectMarketCondition(btcData)
		sb.WriteString(fmt.Sprintf("**BTC**: %.2f (1h: %+.2f%%, 4h: %+.2f%%) | MACD: %.4f | RSI: %.2f | 市场状态: %s(%d%%)\n\n",
			btcData.CurrentPrice, btcData.PriceChange1h, btcData.PriceChange4h,
			btcData.CurrentMACD, btcData.CurrentRSI7, 
			btcCondition.Condition, btcCondition.Confidence))
	}

	// 账户
	sb.WriteString(fmt.Sprintf("**账户**: 净值%.2f | 余额%.2f (%.1f%%) | 盈亏%+.2f%% | 保证金%.1f%% | 持仓%d个\n\n",
		ctx.Account.TotalEquity,
		ctx.Account.AvailableBalance,
		(ctx.Account.AvailableBalance/ctx.Account.TotalEquity)*100,
		ctx.Account.TotalPnLPct,
		ctx.Account.MarginUsedPct,
		ctx.Account.PositionCount))

	// 持仓（完整市场数据）
	if len(ctx.Positions) > 0 {
		sb.WriteString("## 当前持仓\n")
		for i, pos := range ctx.Positions {
			// 计算持仓时长
			holdingDuration := ""
			if pos.UpdateTime > 0 {
				durationMs := time.Now().UnixMilli() - pos.UpdateTime
				durationMin := durationMs / (1000 * 60) // 转换为分钟
				if durationMin < 60 {
					holdingDuration = fmt.Sprintf(" | 持仓时长%d分钟", durationMin)
				} else {
					durationHour := durationMin / 60
					durationMinRemainder := durationMin % 60
					holdingDuration = fmt.Sprintf(" | 持仓时长%d小时%d分钟", durationHour, durationMinRemainder)
				}
			}

			sb.WriteString(fmt.Sprintf("%d. %s %s | 入场价%.4f 当前价%.4f | 盈亏%+.2f%% | 杠杆%dx | 保证金%.0f | 强平价%.4f%s\n\n",
				i+1, pos.Symbol, strings.ToUpper(pos.Side),
				pos.EntryPrice, pos.MarkPrice, pos.UnrealizedPnLPct,
				pos.Leverage, pos.MarginUsed, pos.LiquidationPrice, holdingDuration))

			// 使用Format输出完整市场数据
			if marketData, ok := ctx.MarketDataMap[pos.Symbol]; ok {
				sb.WriteString(market.Format(marketData))
				sb.WriteString("\n")
			}
		}
	} else {
		sb.WriteString("**当前持仓**: 无\n\n")
	}

	// 候选币种（完整市场数据）
	sb.WriteString(fmt.Sprintf("## 候选币种 (%d个)\n\n", len(ctx.MarketDataMap)))
	displayedCount := 0
	for _, coin := range ctx.CandidateCoins {
		marketData, hasData := ctx.MarketDataMap[coin.Symbol]
		if !hasData {
			continue
		}
		displayedCount++

		sourceTags := ""
		if len(coin.Sources) > 1 {
			sourceTags = " (AI500+OI_Top双重信号)"
		} else if len(coin.Sources) == 1 && coin.Sources[0] == "oi_top" {
			sourceTags = " (OI_Top持仓增长)"
		}

		// 使用Format输出完整市场数据
		sb.WriteString(fmt.Sprintf("### %d. %s%s\n\n", displayedCount, coin.Symbol, sourceTags))
		sb.WriteString(market.Format(marketData))
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	// 夏普比率（直接传值，不要复杂格式化）
	if ctx.Performance != nil {
		// 直接从interface{}中提取SharpeRatio
		type PerformanceData struct {
			SharpeRatio float64 `json:"sharpe_ratio"`
		}
		var perfData PerformanceData
		if jsonData, err := json.Marshal(ctx.Performance); err == nil {
			if err := json.Unmarshal(jsonData, &perfData); err == nil {
				sb.WriteString(fmt.Sprintf("## 📊 夏普比率: %.2f\n\n", perfData.SharpeRatio))
			}
		}
	}

	// ==================== 新增：市场状态摘要 ====================
	sb.WriteString("## 🌊 市场状态摘要\n")
	trendingCount, rangingCount, volatileCount := 0, 0, 0
	for symbol, data := range ctx.MarketDataMap {
		if symbol == "BTCUSDT" {
			continue // BTC已经在上面显示过了
		}
		condition := market.DetectMarketCondition(data)
		switch condition.Condition {
		case "trending":
			trendingCount++
		case "ranging":
			rangingCount++
		case "volatile":
			volatileCount++
		}
	}
	
	sb.WriteString(fmt.Sprintf("- 📈 趋势市: %d个币种\n", trendingCount))
	sb.WriteString(fmt.Sprintf("- 🔄 震荡市: %d个币种\n", rangingCount))
	sb.WriteString(fmt.Sprintf("- 🌊 波动市: %d个币种\n\n", volatileCount))
	
	if rangingCount > len(ctx.MarketDataMap)/2 {
		sb.WriteString("🚨 **市场整体处于震荡状态**：建议谨慎开仓，耐心等待趋势突破！\n\n")
	}

	sb.WriteString("---\n\n")
	sb.WriteString("现在请分析并输出决策（思维链 + JSON）\n")

	return sb.String()
}

// parseFullDecisionResponse 解析AI的完整决策响应
func parseFullDecisionResponse(aiResponse string, accountEquity float64, btcEthLeverage, altcoinLeverage int) (*FullDecision, error) {
	// 1. 提取思维链
	cotTrace := extractCoTTrace(aiResponse)

	// 2. 提取JSON决策列表
	decisions, err := extractDecisions(aiResponse)
	if err != nil {
		return &FullDecision{
			CoTTrace:  cotTrace,
			Decisions: []Decision{},
		}, fmt.Errorf("提取决策失败: %w\n\n=== AI思维链分析 ===\n%s", err, cotTrace)
	}

	// 3. 验证决策
	if err := validateDecisions(decisions, accountEquity, btcEthLeverage, altcoinLeverage); err != nil {
		return &FullDecision{
			CoTTrace:  cotTrace,
			Decisions: decisions,
		}, fmt.Errorf("决策验证失败: %w\n\n=== AI思维链分析 ===\n%s", err, cotTrace)
	}

	return &FullDecision{
		CoTTrace:  cotTrace,
		Decisions: decisions,
	}, nil
}

// extractCoTTrace 提取思维链分析
func extractCoTTrace(response string) string {
	// 查找JSON数组的开始位置
	jsonStart := strings.Index(response, "[")
	if jsonStart > 0 {
		// 思维链是JSON数组之前的内容
		return strings.TrimSpace(response[:jsonStart])
	}

	// 如果找不到JSON，整个响应都是思维链
	return strings.TrimSpace(response)
}

// extractDecisions 提取JSON决策列表
func extractDecisions(response string) ([]Decision, error) {
	// 直接查找JSON数组 - 找第一个完整的JSON数组
	arrayStart := strings.Index(response, "[")
	if arrayStart == -1 {
		return nil, fmt.Errorf("无法找到JSON数组起始")
	}

	// 从 [ 开始，匹配括号找到对应的 ]
	arrayEnd := findMatchingBracket(response, arrayStart)
	if arrayEnd == -1 {
		return nil, fmt.Errorf("无法找到JSON数组结束")
	}

	jsonContent := strings.TrimSpace(response[arrayStart : arrayEnd+1])

	// 🔧 修复常见的JSON格式错误：替换中文引号
	jsonContent = fixMissingQuotes(jsonContent)

	// 解析JSON
	var decisions []Decision
	if err := json.Unmarshal([]byte(jsonContent), &decisions); err != nil {
		return nil, fmt.Errorf("JSON解析失败: %w\nJSON内容: %s", err, jsonContent)
	}

	return decisions, nil
}

// fixMissingQuotes 替换中文引号为英文引号（避免输入法自动转换）
func fixMissingQuotes(jsonStr string) string {
	jsonStr = strings.ReplaceAll(jsonStr, "\u201c", "\"") // "
	jsonStr = strings.ReplaceAll(jsonStr, "\u201d", "\"") // "
	jsonStr = strings.ReplaceAll(jsonStr, "\u2018", "'")  // '
	jsonStr = strings.ReplaceAll(jsonStr, "\u2019", "'")  // '
	return jsonStr
}

// validateDecisions 验证所有决策（需要账户信息和杠杆配置）
func validateDecisions(decisions []Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int) error {
	for i, decision := range decisions {
		if err := validateDecision(&decision, accountEquity, btcEthLeverage, altcoinLeverage); err != nil {
			return fmt.Errorf("决策 #%d 验证失败: %w", i+1, err)
		}
	}
	return nil
}

// findMatchingBracket 查找匹配的右括号
func findMatchingBracket(s string, start int) int {
	if start >= len(s) || s[start] != '[' {
		return -1
	}

	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}

	return -1
}

// validateDecision 验证单个决策的有效性
func validateDecision(d *Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int) error {
	// 验证action
	validActions := map[string]bool{
		"open_long":   true,
		"open_short":  true,
		"close_long":  true,
		"close_short": true,
		"hold":        true,
		"wait":        true,
	}

	if !validActions[d.Action] {
		return fmt.Errorf("无效的action: %s", d.Action)
	}

	// 开仓操作必须提供完整参数
	if d.Action == "open_long" || d.Action == "open_short" {
		// 根据币种使用配置的杠杆上限
		maxLeverage := altcoinLeverage          // 山寨币使用配置的杠杆
		maxPositionValue := accountEquity * 1.5 // 山寨币最多1.5倍账户净值
		if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
			maxLeverage = btcEthLeverage          // BTC和ETH使用配置的杠杆
			maxPositionValue = accountEquity * 10 // BTC/ETH最多10倍账户净值
		}

		if d.Leverage <= 0 || d.Leverage > maxLeverage {
			return fmt.Errorf("杠杆必须在1-%d之间（%s，当前配置上限%d倍）: %d", maxLeverage, d.Symbol, maxLeverage, d.Leverage)
		}
		if d.PositionSizeUSD <= 0 {
			return fmt.Errorf("仓位大小必须大于0: %.2f", d.PositionSizeUSD)
		}
		// 验证仓位价值上限（加1%容差以避免浮点数精度问题）
		tolerance := maxPositionValue * 0.01 // 1%容差
		if d.PositionSizeUSD > maxPositionValue+tolerance {
			if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
				return fmt.Errorf("BTC/ETH单币种仓位价值不能超过%.0f USDT（10倍账户净值），实际: %.0f", maxPositionValue, d.PositionSizeUSD)
			} else {
				return fmt.Errorf("山寨币单币种仓位价值不能超过%.0f USDT（1.5倍账户净值），实际: %.0f", maxPositionValue, d.PositionSizeUSD)
			}
		}
		if d.StopLoss <= 0 || d.TakeProfit <= 0 {
			return fmt.Errorf("止损和止盈必须大于0")
		}

		// 验证止损止盈的合理性
		if d.Action == "open_long" {
			if d.StopLoss >= d.TakeProfit {
				return fmt.Errorf("做多时止损价必须小于止盈价")
			}
		} else {
			if d.StopLoss <= d.TakeProfit {
				return fmt.Errorf("做空时止损价必须大于止盈价")
			}
		}

		// 验证风险回报比（必须≥1:3）
		// 计算入场价（假设当前市价）
		var entryPrice float64
		if d.Action == "open_long" {
			// 做多：入场价在止损和止盈之间
			entryPrice = d.StopLoss + (d.TakeProfit-d.StopLoss)*0.2 // 假设在20%位置入场
		} else {
			// 做空：入场价在止损和止盈之间
			entryPrice = d.StopLoss - (d.StopLoss-d.TakeProfit)*0.2 // 假设在20%位置入场
		}

		var riskPercent, rewardPercent, riskRewardRatio float64
		if d.Action == "open_long" {
			riskPercent = (entryPrice - d.StopLoss) / entryPrice * 100
			rewardPercent = (d.TakeProfit - entryPrice) / entryPrice * 100
			if riskPercent > 0 {
				riskRewardRatio = rewardPercent / riskPercent
			}
		} else {
			riskPercent = (d.StopLoss - entryPrice) / entryPrice * 100
			rewardPercent = (entryPrice - d.TakeProfit) / entryPrice * 100
			if riskPercent > 0 {
				riskRewardRatio = rewardPercent / riskPercent
			}
		}

		// 硬约束：风险回报比必须≥3.0
		if riskRewardRatio < 3.0 {
			return fmt.Errorf("风险回报比过低(%.2f:1)，必须≥3.0:1 [风险:%.2f%% 收益:%.2f%%] [止损:%.2f 止盈:%.2f]",
				riskRewardRatio, riskPercent, rewardPercent, d.StopLoss, d.TakeProfit)
		}
	}

	return nil
}

// ==================== 新增：决策验证和过滤函数 ====================

// ValidateDecisionWithMarketData 使用市场数据验证决策（新增函数）
func ValidateDecisionWithMarketData(decision *Decision, marketData *market.Data, account *AccountInfo) (bool, string) {
	if decision == nil {
		return false, "决策为空"
	}
	
	// 检查市场数据
	if marketData == nil {
		return false, "市场数据不可用"
	}
	
	// 检查震荡市（对开仓操作）
	if decision.Action == "open_long" || decision.Action == "open_short" {
		if shouldAvoid, reason := market.ShouldAvoidTrading(marketData); shouldAvoid {
			return false, fmt.Sprintf("市场状态不适合开仓: %s", reason)
		}
	}
	
	// 检查持仓价值
	if marketData.OpenInterest != nil && marketData.CurrentPrice > 0 {
		oiValue := marketData.OpenInterest.Latest * marketData.CurrentPrice
		oiValueInMillions := oiValue / 1_000_000
		if oiValueInMillions < 15 {
			return false, fmt.Sprintf("持仓价值过低(%.2fM USD < 15M)", oiValueInMillions)
		}
	}
	
	// 检查仓位大小
	if decision.PositionSizeUSD > 0 {
		// 确保单笔风险不超过账户净值的2%
		maxRisk := account.TotalEquity * 0.02
		if decision.RiskUSD > maxRisk {
			return false, fmt.Sprintf("风险过大(%.2f > 最大%.2f)", decision.RiskUSD, maxRisk)
		}
	}
	
	// 检查保证金使用率
	if account.MarginUsedPct > 50 {
		return false, fmt.Sprintf("保证金使用率过高(%.1f%% > 50%%)", account.MarginUsedPct)
	}
	
	return true, "决策有效"
}

// FilterValidDecisions 过滤有效的决策（新增函数）
func FilterValidDecisions(decisions []Decision, marketDataMap map[string]*market.Data, account *AccountInfo) []Decision {
	validDecisions := make([]Decision, 0)
	
	for _, decision := range decisions {
		marketData, exists := marketDataMap[decision.Symbol]
		if !exists {
			continue
		}
		
		if valid, _ := ValidateDecisionWithMarketData(&decision, marketData, account); valid {
			validDecisions = append(validDecisions, decision)
		}
	}
	
	return validDecisions
}

// GetDecisionSummary 获取决策摘要（新增函数）
func GetDecisionSummary(decision *FullDecision) string {
	if decision == nil || len(decision.Decisions) == 0 {
		return "🤔 无交易决策"
	}
	
	var sb strings.Builder
	sb.WriteString("🎯 交易决策摘要:\n")
	
	for _, d := range decision.Decisions {
		actionEmoji := getActionEmoji(d.Action)
		sb.WriteString(fmt.Sprintf("%s %s: %s", actionEmoji, d.Symbol, d.Action))
		
		if d.PositionSizeUSD > 0 {
			sb.WriteString(fmt.Sprintf(" | 仓位: $%.2f", d.PositionSizeUSD))
		}
		if d.Leverage > 0 {
			sb.WriteString(fmt.Sprintf(" | 杠杆: %dx", d.Leverage))
		}
		if d.Confidence > 0 {
			sb.WriteString(fmt.Sprintf(" | 信心: %d%%", d.Confidence))
		}
		sb.WriteString("\n")
		
		if d.Reasoning != "" {
			sb.WriteString(fmt.Sprintf("   📝 理由: %s\n", d.Reasoning))
		}
	}
	
	return sb.String()
}

// getActionEmoji 获取动作对应的emoji（新增函数）
func getActionEmoji(action string) string {
	switch action {
	case "open_long":
		return "🟢"
	case "open_short":
		return "🔴"
	case "close_long", "close_short":
		return "🟡"
	case "hold":
		return "🟣"
	case "wait":
		return "🔵"
	default:
		return "⚪"
	}
}

// AnalyzeMarketConditions 分析整体市场状态（新增函数）
func AnalyzeMarketConditions(ctx *Context) string {
	var sb strings.Builder
	
	trendingCount, rangingCount, volatileCount := 0, 0, 0
	var rangingSymbols []string
	
	for symbol, data := range ctx.MarketDataMap {
		condition := market.DetectMarketCondition(data)
		switch condition.Condition {
		case "trending":
			trendingCount++
		case "ranging":
			rangingCount++
			rangingSymbols = append(rangingSymbols, symbol)
		case "volatile":
			volatileCount++
		}
	}
	
	total := len(ctx.MarketDataMap)
	if total == 0 {
		return "无市场数据"
	}
	
	sb.WriteString(fmt.Sprintf("🌊 市场状态分析 (%d个币种):\n", total))
	sb.WriteString(fmt.Sprintf("📈 趋势市: %d (%.1f%%)\n", trendingCount, float64(trendingCount)/float64(total)*100))
	sb.WriteString(fmt.Sprintf("🔄 震荡市: %d (%.1f%%)\n", rangingCount, float64(rangingCount)/float64(total)*100))
	sb.WriteString(fmt.Sprintf("🌊 波动市: %d (%.1f%%)\n", volatileCount, float64(volatileCount)/float64(total)*100))
	
	if rangingCount > total/2 {
		sb.WriteString("\n🚨 **市场警告**: 超过50%的币种处于震荡状态！\n")
		sb.WriteString("建议策略:\n")
		sb.WriteString("• 避免新开仓位\n")
		sb.WriteString("• 现有持仓考虑减仓\n")
		sb.WriteString("• 耐心等待趋势突破\n")
	}
	
	if len(rangingSymbols) > 0 {
		sb.WriteString(fmt.Sprintf("\n🔄 震荡币种: %s\n", strings.Join(rangingSymbols, ", ")))
	}
	
	return sb.String()
}
