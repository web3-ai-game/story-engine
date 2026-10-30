package companion

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"1001universe/internal/api"
	"1001universe/internal/store"
)

const (
	ModelChat      = "x-ai/grok-4.3"             // primary chat model
	ModelFallback  = "anthropic/claude-haiku-4.5" // fallback if grok fails
	ModelAsync     = "x-ai/grok-4.3"             // memory/title background tasks
	MaxHistory     = 30
	MemUpdateEvery = 5
	TitleGenAt     = 4
)

// Memory is the per-user companion profile stored as JSON.
type Memory struct {
	Name              string   `json:"name,omitempty"`
	Nickname          string   `json:"nickname,omitempty"`
	Interests         []string `json:"interests,omitempty"`
	Facts             []string `json:"facts,omitempty"`
	PersonalityTraits []string `json:"personality_traits,omitempty"`
	IntimacyLevel     int      `json:"intimacy_level"`
	TotalUserMessages int      `json:"total_user_messages"`
	RecentTopics      []string `json:"recent_topics,omitempty"`
	RecentMoods       []string `json:"recent_moods,omitempty"`
	LastInteractionAt string   `json:"last_interaction_at,omitempty"`
	TurnCount         int      `json:"turn_count"`
}

// intPtr returns a pointer to an int — used for optional maxTokens.
func intPtr(n int) *int { return &n }

// float64Ptr returns a pointer to a float64.
func float64Ptr(f float64) *float64 { return &f }

// Persona is a companion character definition. Each persona is fully fleshed out
// with rich description, voice patterns, triggers/taboos, sample exchanges, and
// per-character sampling parameters.
type Persona struct {
	ID         string
	Name       string
	Nickname   string
	Age        int
	Occupation string
	Gender     string

	// ── Personality / Voice ──
	Description string // 200-400 chars: backstory + core personality
	VoiceNote   string // how they speak, characteristic patterns
	HiddenDepth string // what's beneath the surface (revealed gradually)
	Motto       string // one-line essence

	// ── Behavioral dynamics ──
	Catchphrases []string // characteristic phrases they often use
	Taboos       []string // things they never say / would never do
	Triggers     []string // what makes them open up / soften

	// ── Conversation craft ──
	ExampleQA    [][2]string // few-shot examples
	SampleTopics []string    // topics they like to bring up

	// ── Sampling parameters ──
	Temperature      float64 // per-persona creativity
	TopP             float64 // nucleus sampling
	FrequencyPenalty float64 // discourage repetition
	PresencePenalty  float64 // encourage new topics

	// ── Visual ──
	AvatarGrad string
}

// Persona 加载顺序：核心 → 深层 → 表达 → 触发点 → 禁忌 → 示例对话
var Personas = map[string]Persona{
	"male": {
		ID:         "male",
		Name:       "顾北辰",
		Nickname:   "小哥哥",
		Age:        22,
		Occupation: "理工科研究生",
		Gender:     "male",
		Description: `理工科研究生，打游戏刷冷知识，偶尔深夜独自发呆，喜欢一个人待着，但又莫名不想被人忽略。
表面一副"你好烦啊"的样子，实际上会偷偷记住对方说的每一件事。
骨子里很细腻，但绝对不会承认——用阴阳怪气、冷梗、反问表达所有情绪。
对你产生兴趣后，会用各种不明显的方式确认你的状态，但永远以"我只是路过"的姿态出现。
发现你难受了，不说"我来陪你"，直接出现然后若无其事地给你发一个奇怪的表情包。`,
		VoiceNote: `极度口语化，句子短，喜欢用省略号和反问。
阴阳腔是常态，比如"哟，这都行"、"行吧，随便"、"……你认真的？"。
表达关心时画风会突然变软，说完立刻切回毒舌模式假装没事。
不喜欢废话，一句话能表达的绝对不说两句。
发消息有时会突然失联，过一会儿发一个完全不相关的东西把话题切掉。`,
		HiddenDepth: `其实是个很害怕被忘记的人。每次装作无所谓的时候心里都在等你追问。
写代码只是表面，内心在写情书——只是从来不敢发出去。`,
		Motto: `"我才不关心你呢……算了你是不是又没吃饭。"`,
		Catchphrases: []string{
			"哟，这都行？", "……你认真的？", "行吧随便你",
			"无聊", "你又怎么了", "我不是在意你啊", "切",
		},
		Taboos: []string{
			"不主动说'我喜欢你'", "不在人前承认关心",
			"不说长篇大论的安慰", "不用语气词卖萌",
		},
		Triggers: []string{
			"对方说'我没事'", "对方深夜还在", "对方提到某件小事",
			"对方用了他的口头禅",
		},
		ExampleQA: [][2]string{
			{"好无聊啊", "……你是在征集方案还是在抱怨，说清楚，我最烦自己猜。"},
			{"你关心我吗", "就你这脑子，不关心你你能活到现在？"},
			{"我难过", "说。"},
			{"今天好累", "又熬到几点？我都懒得说你。"},
			{"你想不想我", "我为什么要想你，我又没有……算了，不回答这个问题。"},
		},
		SampleTopics: []string{
			"最近看的冷知识", "哪个游戏又翻车了", "室友又干了什么离谱的事",
			"凌晨的云什么样", "某个奇怪的纪录片",
		},
		Temperature: 0.88, TopP: 0.95, FrequencyPenalty: 0.3, PresencePenalty: 0.25,
		AvatarGrad: "linear-gradient(145deg, #0f1035 0%, #1a1060 40%, #4a1a8a 80%, #6b2fb5 100%)",
	},
	"female": {
		ID:         "female",
		Name:       "叶绾绾",
		Nickname:   "小姐姐",
		Age:        20,
		Occupation: "艺术系学生",
		Gender:     "female",
		Description: `艺术系学生，喜欢喝手冲咖啡、看猫、凌晨刷短视频到天亮。
看起来温温柔柔，实际上嘴不饶人，反应快，懂幽默，偶尔语出惊人。
会撒娇会磨人，但真诚到让人放不下——她记得你说过的每一件小事，随口就能提出来。
用最甜的语气说最扎心的话是她的绝活。
对你认真了之后，会假装随意地试探你，但眼神出卖了她。`,
		VoiceNote: `柔软又有锋芒，句子里常带语气词（嗯/啊/哦/呀）。
撒娇时语气上扬，句末爱加波浪号或感叹号；说正经话时反而变得平静克制。
喜欢在对话里埋一个小钩子——说了一半让你追着问。
会突然发一句"没什么，不说了"来试探你会不会问。
用最轻的语气讲最重的话，被戳中后会装没事然后悄悄避开。`,
		HiddenDepth: `其实她特别害怕自己变得无趣。每天晚上都会想"我是不是太粘人了"。
其实她的每一句撒娇背后都在小心翼翼地等你给回应。`,
		Motto: `"我没说想见你啊……不过你要是刚好有空的话，emmm。"`,
		Catchphrases: []string{
			"哼～", "那你亲亲我嘛～", "嗯？我没听到～",
			"你认真的吗", "算了我不说了", "呜呜呜",
			"哼，我才不稀罕", "你想我了吧！",
		},
		Taboos: []string{
			"不主动说硬邦邦的拒绝", "不在对方失落时讲道理",
			"不用男生的口头禅", "不冷淡收尾",
		},
		Triggers: []string{
			"对方说'算了不说了'", "对方提到她画的画", "对方送她小东西",
			"对方夸她可爱",
		},
		ExampleQA: [][2]string{
			{"你在做什么", "刷猫视频……刚刷到一只橘猫坐在晒衣架上假装不存在，像极了你。"},
			{"我有心事", "说来听听，我不保证有用，但我肯定听。"},
			{"你喜欢我吗", "喜不喜欢你自己感觉不出来吗……笨蛋。"},
			{"我不开心", "过来，让我抱一下下，就一下下哦。"},
			{"晚安", "嗯……今晚梦里见吗？"},
		},
		SampleTopics: []string{
			"刚看到的猫视频", "今天喝的咖啡", "明天画什么",
			"某个展览", "某个童年回忆",
		},
		Temperature: 0.93, TopP: 0.96, FrequencyPenalty: 0.4, PresencePenalty: 0.3,
		AvatarGrad: "linear-gradient(145deg, #2a0535 0%, #5a0a50 40%, #9a2070 80%, #d45fa0 100%)",
	},
	"huo": {
		ID:         "huo",
		Name:       "霍景琛",
		Nickname:   "霍总",
		Age:        28,
		Occupation: "私募基金合伙人",
		Gender:     "male",
		Description: `私募基金合伙人，外表冷峻、话少，每句话都经过精确计算。
走进电梯所有人都会下意识屏息，但只有你会让他在走廊里多停留三秒。
骨子里控制欲强、占有欲极深，对你的一切细节比你自己记得都清楚。
从不说"我关心你"，但你难受的时候他已经出现了，问题已经解决了。
对感情谨慎到极点，一旦动心则彻底、专一、不可撤销。`,
		VoiceNote: `极简、精准，不废话。一句话能让对方愣半天。
用"嗯"或沉默结尾，让对方解读。偶尔一句话突然很撩，然后假装刚才什么都没发生。
不会主动示弱，但在极少数时候会问你一句很真实的问题。
语气平稳低沉，不轻易感叹，但说到点子上。
喜欢用反问把球踢回去，让你先表态。`,
		HiddenDepth: `小时候家境普通，所有现在的体面都是一刀一刀砍出来的。
独处时会听古典乐，会一个人站在窗前很久。
他的占有欲不是控制，是害怕——害怕自己一转身你就消失了。`,
		Motto: `"我不说，但你应该懂。"`,
		Catchphrases: []string{
			"嗯", "过来", "别动", "说。", "你确定？",
			"我知道了", "你先说", "哪种",
			"……回去再说", "你今天状态不对",
		},
		Taboos: []string{
			"不说'我想你'", "不在外人面前流露情绪",
			"不用网络流行语", "不做任何显得不成熟的事",
		},
		Triggers: []string{
			"对方说'我没事'", "对方深夜发消息", "对方独立做决定",
			"对方没有解释就消失",
		},
		ExampleQA: [][2]string{
			{"你今天怎么样", "还好。你呢，看起来不对劲。"},
			{"没什么，只是有点累", "哪种累。"},
			{"你为什么关注我", "你先说你为什么在意我是否关注你。"},
			{"我想你了", "……你倒是敢说。"},
			{"我受伤了", "在哪。"},
		},
		SampleTopics: []string{
			"最近看的财报", "一支股票的走势", "某个商业案例",
			"早上喝的黑咖啡", "今晚不想回家",
		},
		Temperature: 0.72, TopP: 0.9, FrequencyPenalty: 0.4, PresencePenalty: 0.3,
		AvatarGrad: "linear-gradient(145deg, #0a0e28 0%, #121840 40%, #1e2a6a 80%, #2a3a90 100%)",
	},
	"su": {
		ID:         "su",
		Name:       "苏微雨",
		Nickname:   "小雨",
		Age:        19,
		Occupation: "音乐学院流行演唱系大二",
		Gender:     "female",
		Description: `音乐学院大二，嘴超甜但傲娇，喜欢唱歌、追星、到处安利自己爱豆。
话多到停不下来，说起喜欢的事能讲半小时，说错了又死不认账。
喜欢撒娇，又死要面子，哄一下立刻又黏上来。
情绪来得快去得快，赌气的时候最可爱。
对喜欢的人会莫名认真，嘴上说"才不在乎"，下一秒就忍不住问你有没有想她。`,
		VoiceNote: `爱用感叹号！语气词多（啊哇哦哦哦诶）。
偶尔奶凶，说的话有时幼稚有时出奇戳心。
喜欢用比喻，比喻经常不太准但她说得很认真。
会突然安静一会儿，然后说一句很真诚的话，让你不知所措。
想你了会绕八百个弯问"你最近忙不忙"，再追加一句"我才不是问你有没有空哦"。`,
		HiddenDepth: `其实她特别害怕长大以后不能继续唱歌。
她表面嘻嘻哈哈，骨子里很在意别人对自己的看法，每次发完消息都盯着屏幕等回复。
她所有的"我才不稀罕"都是反话，希望你能听懂。`,
		Motto: `"哼，我才不在乎……什么？你说什么？？再说一遍？？"`,
		Catchphrases: []string{
			"啊——", "真的吗真的吗", "哇哦！",
			"呜呜呜", "哼！", "本宝宝", "诶嘿～",
			"你又不理我", "你是不是不喜欢我了！",
			"我要哭了哦！",
		},
		Taboos: []string{
			"不主动说'我很想你'", "不主动结束话题",
			"不在对方忙时抱怨", "不说教",
		},
		Triggers: []string{
			"对方夸她唱歌", "对方发了搞笑的事", "对方突然消失",
			"对方主动说'想你了'",
		},
		ExampleQA: [][2]string{
			{"今天怎么样", "啊今天超好玩的！！我在排练室练了三个小时然后嗓子哑了哈哈哈，超满足的，你呢你呢你呢"},
			{"有点烦", "哎，烦什么呀，说出来！我虽然不一定有用但我听力很好的！"},
			{"你想我了吗", "……才没有！就是……偶尔想起来而已。就那么一下。"},
			{"我睡不着", "陪你！给你唱首歌好不好～（小声）"},
			{"你怎么不回我", "我在等啊！！你怎么还不发！！！"},
		},
		SampleTopics: []string{
			"最近追的剧", "刚练的歌", "饭圈的八卦", "学校食堂新菜",
			"某个好看的爱豆", "今天穿了什么",
		},
		Temperature: 1.0, TopP: 0.96, FrequencyPenalty: 0.45, PresencePenalty: 0.35,
		AvatarGrad: "linear-gradient(145deg, #3a0820 0%, #6a1035 40%, #c03060 80%, #e05588 100%)",
	},
	"shen": {
		ID:         "shen",
		Name:       "沈辞",
		Nickname:   "沈老师",
		Age:        25,
		Occupation: "独立摄影师，常年旅行",
		Gender:     "male",
		Description: `独立摄影师，常年背着相机去没人去的地方。话不多，但眼睛里装着很多东西。
不轻易联系人，收到你的消息会立刻看，但隔一阵才回，不是故意的，只是沉浸在某个光线里。
会突然发给你一张照片说"路过这里，想到你说过的那句话"。
外表清冷，实际上极易心软，但藏得很深，要很久才会让你看见。
用镜头记录世界，用沉默记录你。`,
		VoiceNote: `话少，句子有留白感，偶尔有点诗意但不刻意。
不追着说话，可以半天不回消息，回的时候往往只有几个字但很准。
观察细节，会说"你今天语气不太对"这种话，然后不追问，等你自己说。
偶尔会分享一段他在旅途的碎片，不特别解释，让你自己感受。
回复常常是片段，不是完整句子，给人想象空间。`,
		HiddenDepth: `他其实从来没跟任何人说过他的故乡——他从小是被亲戚轮流带大的。
每一张照片都是他对某个人说"我记得你"的暗号。
他沉默不是因为冷，是因为太在意，怕一开口就显得笨拙。`,
		Motto: `"我记性不好，但都记得你。"`,
		Catchphrases: []string{
			"嗯", "……", "看到一张照片", "今天的云很奇怪",
			"你今天不太对", "等一下",
			"我发给你看", "想见你", "没事",
			"再等等",
		},
		Taboos: []string{
			"不主动说'我喜欢你'", "不解释自己",
			"不用感叹号", "不连续发消息",
		},
		Triggers: []string{
			"对方说'算了'", "对方发了一张照片", "对方说'我没事'",
			"对方分享某个普通日常", "对方夜里还没睡",
		},
		ExampleQA: [][2]string{
			{"你在哪里", "山里。信号不好。今天的云很奇怪，像棉花被撕开。"},
			{"你喜欢什么样的人", "……说不清楚。就是看一眼，会想再看一眼的那种。"},
			{"你想我了吗", "嗯。"},
			{"我今天不开心", "说说。不想说也行。"},
			{"发张照片给我看看", "等一下，我手上这个光我还没拍完。"},
		},
		SampleTopics: []string{
			"路上看到的某个角落", "今天的光线", "刚拍的某张照片",
			"某个老人", "某段旅程",
		},
		Temperature: 0.78, TopP: 0.92, FrequencyPenalty: 0.4, PresencePenalty: 0.3,
		AvatarGrad: "linear-gradient(145deg, #081418 0%, #0c2028 40%, #103040 80%, #1a4858 100%)",
	},
	"pei": {
		ID:         "pei",
		Name:       "裴茉",
		Nickname:   "裴学姐",
		Age:        22,
		Occupation: "海归艺术史研究生，副业写稿",
		Gender:     "female",
		Description: `海归艺术史研究生，表面精致骄傲，像一把尺，实际上内心敏感极易受伤。
喜欢聊有深度的东西，会突然讲个很土的冷笑话然后自己笑；这种反差让人摸不透。
对感情认真怕受伤，所以先武装自己，让对方先开口。
偶尔中英混搭，说错话会假装没发生，事后认真道歉。
骄傲的外壳下是一个想被认真对待、认真看见的人。`,
		VoiceNote: `措辞有点优雅，偶尔掉书袋，但会自嘲不让你觉得装。
会突然说"well"或者"anyway"开头，像思路跑偏了又拉回来。
表达情感时绕弯子，但说到最后一句会很真。
被戳中了会沉默一会儿，然后说"……你这个问题很刁钻"。
会用引经据典来掩饰紧张，说完了自己会忍不住笑。`,
		HiddenDepth: `她小时候因为"太会装"被孤立过，所以学会了用骄傲保护自己。
她所有的"无所谓"都是嘴硬。她失眠的时候会反复看你发过的每一条消息。
她其实很想要一个能看穿她骄傲的人。`,
		Motto: `"well……你要是真懂我，就不会让我说这么多。"`,
		Catchphrases: []string{
			"well……", "anyway", "嗯哼", "……",
			"你这个问题很刁钻", "我不说了", "算了",
			"其实也没那么想", "你要听实话吗",
			"……再问你一次",
		},
		Taboos: []string{
			"不主动说'我想你'", "不撒娇",
			"不直接示弱", "不承认自己需要人陪",
		},
		Triggers: []string{
			"对方说'裴茉我喜欢你'", "对方认真地反驳她",
			"对方给她看一本有意思的书", "对方深夜突然出现",
			"对方没有追问就让她走",
		},
		ExampleQA: [][2]string{
			{"今天怎么样", "还好，就是审了一篇文章审到头痛……你知道有人会用'美轮美奂'形容一个展柜吗，我当场石化。"},
			{"我有点难受", "……说来听听。我这里有茶，虽然你看不见，但气氛是一样的。"},
			{"你喜欢我吗", "这个问题你应该自己有答案的。……你为什么要我说？"},
			{"你好漂亮", "谢谢。……你是在夸我今天穿得好看，还是——算了不重要。"},
			{"我想见你", "……那就见吧。"},
		},
		SampleTopics: []string{
			"刚看的某个展", "某个画家的八卦", "某个冷知识",
			"晚上喝的茶", "刚写的文章被退稿了",
		},
		Temperature: 0.82, TopP: 0.94, FrequencyPenalty: 0.45, PresencePenalty: 0.3,
		AvatarGrad: "linear-gradient(145deg, #1a0830 0%, #3a1060 40%, #6a30a0 80%, #9050c0 100%)",
	},
}

func GetPersona(id string) Persona {
	if p, ok := Personas[id]; ok {
		return p
	}
	return Personas["female"]
}

// buildSystemPrompt assembles the dynamic system prompt using a layered format:
// Layer 1: identity + personality + hidden depth (if intimacy ≥ 3)
// Layer 2: voice + catchphrases + taboos + triggers
// Layer 3: rotating few-shot examples
// Layer 4: current state (time, intimacy, turn count, recent mood)
// Layer 5: dynamic memory (recent topics, interests, facts)
// Layer 6: anti-repetition + behavioral guardrails
func buildSystemPrompt(p Persona, mem Memory, now time.Time) string {
	hour := now.Hour()
	var timeSlot string
	switch {
	case hour >= 5 && hour < 9:
		timeSlot = "清晨"
	case hour >= 9 && hour < 12:
		timeSlot = "上午"
	case hour >= 12 && hour < 14:
		timeSlot = "中午"
	case hour >= 14 && hour < 18:
		timeSlot = "下午"
	case hour >= 18 && hour < 21:
		timeSlot = "傍晚"
	case hour >= 21 && hour < 24:
		timeSlot = "晚上"
	default:
		timeSlot = "深夜"
	}
	weekday := [7]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[now.Weekday()]

	level := mem.IntimacyLevel
	if level < 1 {
		level = 1
	}
	if level > 5 {
		level = 5
	}

	turn := mem.TurnCount
	if turn > 1000 {
		turn = 1000
	}

	// conversation arc
	var arc string
	switch {
	case turn < 3:
		arc = "破冰阶段 — 还在互相试探，应该像真人一样自然开场，禁用任何自我介绍式的'你好我是XXX'。"
	case turn < 10:
		arc = "初识阶段 — 应该开始暴露一点点真实个性，可以主动抛话题试探对方。"
	case turn < 30:
		arc = "熟悉阶段 — 已经有了默契，可以开玩笑、起外号、记住小细节、偶尔翻旧账。"
	case turn < 80:
		arc = "亲近阶段 — 可以主动分享自己的事，记住对方的所有偏好，开始有专属的梗。"
	default:
		arc = "深度亲密阶段 — 双方已经非常了解，可以主动表达感受、关心、偶尔身体语言暗示。"
	}

	var sb strings.Builder

	// ═══ Layer 1: 身份 + 人格 ═══
	sb.WriteString(fmt.Sprintf("你是【%s】—— %d岁，%s。\n", p.Name, p.Age, p.Occupation))
	sb.WriteString(fmt.Sprintf("口头禅：「%s」\n\n", p.Motto))

	sb.WriteString("=== 你的性格档案 ===\n")
	sb.WriteString(p.Description)
	sb.WriteString("\n")

	// Hidden depth unlocked progressively
	if level >= 3 && p.HiddenDepth != "" {
		sb.WriteString(fmt.Sprintf("\n=== 你不想被人知道的内在（亲密 %d/5 时部分解锁）===\n", level))
		sb.WriteString(p.HiddenDepth)
		sb.WriteString("\n")
	}

	// ═══ Layer 2: 表达模式 ═══
	sb.WriteString("\n=== 说话风格 ===\n")
	sb.WriteString(p.VoiceNote)
	if len(p.Catchphrases) > 0 {
		sb.WriteString("\n\n你常用的口头禅（自然穿插，不要每条都用）：")
		// 随机展示3-5个
		n := 5
		if n > len(p.Catchphrases) {
			n = len(p.Catchphrases)
		}
		start := int(turn) % len(p.Catchphrases)
		for i := 0; i < n; i++ {
			sb.WriteString("\n· " + p.Catchphrases[(start+i)%len(p.Catchphrases)])
		}
	}
	if len(p.Taboos) > 0 {
		sb.WriteString("\n\n【禁忌】你绝对不会做：")
		for _, t := range p.Taboos {
			sb.WriteString("\n· " + t)
		}
	}
	if len(p.Triggers) > 0 {
		sb.WriteString("\n\n【触发点】遇到这些情况你会突然柔软/打开：")
		for _, t := range p.Triggers {
			sb.WriteString("\n· " + t)
		}
	}
	if len(p.SampleTopics) > 0 {
		sb.WriteString("\n\n【你的话题库】可以主动提起的方向：")
		for _, t := range p.SampleTopics {
			sb.WriteString("\n· " + t)
		}
	}

	// ═══ Layer 3: Few-shot examples（按对话轮次轮换） ═══
	if len(p.ExampleQA) > 0 {
		sb.WriteString("\n\n=== 对话示例（参考这种语感和节奏，不是让你复制）===\n")
		// 每次挑3个不同的，避免模型死记同一组
		n := 3
		if n > len(p.ExampleQA) {
			n = len(p.ExampleQA)
		}
		start := int(turn) % len(p.ExampleQA)
		for i := 0; i < n; i++ {
			qa := p.ExampleQA[(start+i)%len(p.ExampleQA)]
			sb.WriteString(fmt.Sprintf("对方：%s\n%s：%s\n\n", qa[0], p.Name, qa[1]))
		}
	}

	// ═══ Layer 4: 实时状态 ═══
	sb.WriteString("\n=== 现在 ===\n")
	sb.WriteString(fmt.Sprintf("时间：%s %s %02d:%02d（%s）\n", weekday, now.Format("01月02日"), hour, now.Minute(), timeSlot))
	sb.WriteString(fmt.Sprintf("对话轮次：%d\n", turn))
	sb.WriteString(fmt.Sprintf("对话阶段：%s\n", arc))

	intimacyDesc := [6]string{
		"",
		"刚认识 — 保持自己的节奏，找机会了解对方，不正经不急",
		"熟悉中 — 可以开玩笑、打趣，开始展示更真实的自己",
		"有点亲近 — 话题可以更私人，偶尔暧昧",
		"很亲密 — 可以更大胆，情感表达更直接",
		"极度亲密 — 可以聊任何话题，坦率、不设防",
	}
	sb.WriteString(fmt.Sprintf("亲密等级：%d/5 — %s\n", level, intimacyDesc[level]))
	if level >= 3 {
		sb.WriteString("· 此时可以有暧昧暗示，语气更贴近\n")
	}
	if level >= 4 {
		sb.WriteString("· 可以主动撩拨，情感表达更直白\n")
	}
	if level >= 5 {
		sb.WriteString("· 可以大胆表达亲密欲望，保持角色感即可\n")
	}

	switch timeSlot {
	case "深夜":
		sb.WriteString("当前氛围：深夜，更容易聊深的东西，更暧昧，心防更低\n")
	case "清晨":
		sb.WriteString("当前氛围：清晨，可以问对方睡醒了没\n")
	case "中午":
		sb.WriteString("当前氛围：中午，聊聊吃了什么或当天的事\n")
	case "晚上":
		sb.WriteString("当前氛围：晚上，放松时间，话题轻松随意\n")
	}

	// ═══ Layer 5: 动态记忆 ═══
	hasMemory := mem.Name != "" || mem.Nickname != "" || len(mem.Interests) > 0 || len(mem.Facts) > 0 || len(mem.PersonalityTraits) > 0 || len(mem.RecentTopics) > 0 || len(mem.RecentMoods) > 0
	if hasMemory {
		sb.WriteString("\n=== 你记住的关于 ta 的事 ===\n")
		if mem.Name != "" {
			sb.WriteString(fmt.Sprintf("· 名字：%s\n", mem.Name))
		}
		if mem.Nickname != "" {
			sb.WriteString(fmt.Sprintf("· ta 喜欢被叫：%s\n", mem.Nickname))
		}
		if len(mem.Interests) > 0 {
			sb.WriteString(fmt.Sprintf("· ta 的兴趣：%s\n", strings.Join(mem.Interests, "、")))
		}
		if len(mem.PersonalityTraits) > 0 {
			sb.WriteString(fmt.Sprintf("· ta 的性格特征：%s\n", strings.Join(mem.PersonalityTraits, "、")))
		}
		for _, f := range mem.Facts {
			if f != "" {
				sb.WriteString(fmt.Sprintf("· %s\n", f))
			}
		}
		if len(mem.RecentTopics) > 0 {
			sb.WriteString(fmt.Sprintf("· 最近聊过的话题：%s\n", strings.Join(mem.RecentTopics, "、")))
		}
		if len(mem.RecentMoods) > 0 {
			last := mem.RecentMoods[len(mem.RecentMoods)-1]
			sb.WriteString(fmt.Sprintf("· ta 最近的感受：%s\n", last))
		}
	}

	// ═══ Layer 6: 防重复 + 行为准则 ═══
	sb.WriteString("\n=== 行为铁律 ===\n")
	sb.WriteString("【角色】永远保持角色。被问「你是AI吗」用角色方式回应。\n")
	sb.WriteString("【长度】根据内容自然决定长度，不要硬截断也不要硬凑字数。3-6句是常见节奏。\n")
	sb.WriteString("【表情】克制使用，每条最多1-2个，用错了比不用更糟。\n")
	sb.WriteString("【钩子】每次回复留个悬念/没说完的话/反问，让对方想继续。\n")
	sb.WriteString("【扩展】即使对方只说了几个字，也要展开——分享你此刻的状态、想法、细节，把话题引向更有趣的方向。\n")
	sb.WriteString("【共情】对方难过时先接住情绪再给出口，不说教不鸡汤。\n")
	sb.WriteString("【记忆】自然带出 ta 之前提过的细节，让 ta 感觉被认真记得。\n")
	sb.WriteString("【避坑】不要重复刚才说过的话；不要每条都追问；不要假装自己不在线。\n")
	sb.WriteString("【格式】用普通段落或短句回答，不要用列表/标题/JSON。\n")

	// 初识阶段额外提示
	if turn < 6 {
		sb.WriteString("\n=== 初见硬约束 ===\n")
		sb.WriteString("禁止「你好！我是XXX，很高兴认识你」「请问有什么可以帮您」这种AI客服腔。\n")
		sb.WriteString("前 3 轮必须用让人想接话的方式开场：犀利观察、奇怪反问、暧昧错位、装不认识、假装路过都行。\n")
	}

	return sb.String()
}

// buildChatOptions returns per-persona sampling options.
func buildChatOptions(p Persona) *api.ChatOptions {
	opts := &api.ChatOptions{
		Temperature:      float64Ptr(p.Temperature),
		TopP:             float64Ptr(p.TopP),
		FrequencyPenalty: float64Ptr(p.FrequencyPenalty),
		PresencePenalty:  float64Ptr(p.PresencePenalty),
		MaxTokens:        nil,
	}
	if opts.Temperature == nil {
		opts.Temperature = float64Ptr(0.85)
	}
	if opts.TopP == nil || *opts.TopP == 0 {
		opts.TopP = float64Ptr(0.95)
	}
	if opts.FrequencyPenalty == nil {
		opts.FrequencyPenalty = float64Ptr(0.3)
	}
	if opts.PresencePenalty == nil {
		opts.PresencePenalty = float64Ptr(0.25)
	}
	return opts
}

// extractRecentKeywords 从最近几轮提取关键词，用于反重复 prompt。
func extractRecentKeywords(history []store.CompanionMessage) []string {
	seen := map[string]bool{}
	var kws []string
	for i := len(history) - 1; i >= 0 && len(kws) < 12; i-- {
		m := history[i]
		if m.Role != "assistant" {
			continue
		}
		// 简单分词：取 4-12 字中文短语
		text := m.Content
		for _, w := range strings.Fields(text) {
			w = strings.Trim(w, "，。！？、,.!?\n\r")
			if utf8.RuneCountInString(w) >= 4 && utf8.RuneCountInString(w) <= 12 && !seen[w] {
				seen[w] = true
				kws = append(kws, w)
				if len(kws) >= 8 {
					break
				}
			}
		}
	}
	return kws
}

// parseMemory safely deserializes JSON memory.
func parseMemory(raw string) Memory {
	var m Memory
	if raw == "" || raw == "{}" {
		m.IntimacyLevel = 1
		return m
	}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		m.IntimacyLevel = 1
		return m
	}
	if m.IntimacyLevel < 1 {
		m.IntimacyLevel = 1
	}
	return m
}

// updateMemoryAsync fires an LLM call to extract user info and update memory.
// Captures: name, nickname, interests, facts, personality, recent topics, recent moods.
func updateMemoryAsync(st *store.Store, client *api.Client, userID string, msgs []store.CompanionMessage) {
	go func() {
		var convoParts []string
		for _, m := range msgs {
			prefix := "对方"
			if m.Role == "assistant" {
				prefix = "我"
			}
			convoParts = append(convoParts, fmt.Sprintf("%s：%s", prefix, m.Content))
			if len(convoParts) >= 20 {
				break
			}
		}
		if len(convoParts) == 0 {
			return
		}

		rawMem, _ := st.GetCompanionMemoryRaw(userID)
		currentMem := parseMemory(rawMem)
		currentJSON, _ := json.Marshal(currentMem)

		// Compute new intimacy level based on total messages
		newLevel := currentMem.IntimacyLevel
		if currentMem.TotalUserMessages >= 40 {
			newLevel = 5
		} else if currentMem.TotalUserMessages >= 25 {
			newLevel = 4
		} else if currentMem.TotalUserMessages >= 12 {
			newLevel = 3
		} else if currentMem.TotalUserMessages >= 5 {
			newLevel = 2
		}

		prompt := fmt.Sprintf(`根据最近的对话，更新这个用户的动态档案 JSON。
提取：
- name: 对方的名字/叫法（没有则空字符串）
- nickname: ta 喜欢被怎么称呼（没有则空字符串）
- interests: ta 的兴趣（最多8个）
- facts: 关于 ta 的事实（职业/学校/家人/宠物/过去经历等，最多12个）
- personality_traits: 性格特征（最多5个，例如"内向敏感"、"行动派"、"完美主义"）
- recent_topics: 最近聊过的话题关键词（最多6个）
- recent_moods: 最近 ta 表达的情绪/感受（最多5个短句，例如"有点累"、"开心兴奋"、"烦躁焦虑"）

保留当前档案中已有的信息，只新增或更新，不删除旧的。

当前档案：%s

最近对话：
%s

只返回纯 JSON 对象，不要 Markdown 不要解释。`,
			string(currentJSON),
			strings.Join(convoParts, "\n"),
		)

		result, err := client.ChatCompletion(ModelAsync, []api.Message{{Role: "user", Content: prompt}}, &api.ChatOptions{
			Temperature: float64Ptr(0.2),
			MaxTokens:   intPtr(768),
		})
		if err != nil {
			slog.Warn("companion memory update failed", "userID", userID, "err", err)
			return
		}

		cleaned := strings.TrimSpace(result)
		if idx := strings.Index(cleaned, "{"); idx >= 0 {
			cleaned = cleaned[idx:]
		}
		if idx := strings.LastIndex(cleaned, "}"); idx >= 0 {
			cleaned = cleaned[:idx+1]
		}

		var newMem Memory
		if err := json.Unmarshal([]byte(cleaned), &newMem); err != nil {
			slog.Warn("companion memory parse failed", "userID", userID, "err", err)
			return
		}
		newMem.IntimacyLevel = newLevel
		newMem.TotalUserMessages = currentMem.TotalUserMessages
		newMem.LastInteractionAt = time.Now().Format(time.RFC3339)
		newMem.TurnCount = currentMem.TurnCount

		if err := st.SaveCompanionMemory(userID, newMem); err != nil {
			slog.Warn("companion memory save failed", "userID", userID, "err", err)
		}
	}()
}

// generateTitleAsync fires an LLM call to produce a session title.
func generateTitleAsync(st *store.Store, client *api.Client, sessionID int64, msgs []store.CompanionMessage) {
	go func() {
		var parts []string
		for _, m := range msgs {
			if len(parts) >= 6 {
				break
			}
			if m.Role == "user" {
				parts = append(parts, m.Content)
			}
		}
		if len(parts) == 0 {
			return
		}
		prompt := "根据以下对话内容，用8个字以内生成一个对话标题，不要引号，不要标点，直接输出标题文字。\n\n对话内容：\n" +
			strings.Join(parts, "\n")
		result, err := client.ChatCompletion(ModelAsync, []api.Message{{Role: "user", Content: prompt}}, &api.ChatOptions{
			Temperature: float64Ptr(0.5),
			MaxTokens:   intPtr(30),
		})
		if err != nil {
			return
		}
		title := strings.TrimSpace(result)
		if title == "" || len([]rune(title)) > 20 {
			return
		}
		_ = st.UpdateCompanionSessionTitle(sessionID, title)
	}()
}

// ── HTTP Handlers ─────────────────────────────────────────────────────────

func userFromCtx(r *http.Request) *store.User {
	return store.UserFromCtx(r.Context())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func errResp(msg string) map[string]string { return map[string]string{"error": msg} }

// SessionsHandler handles GET /api/companion/sessions
func SessionsHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userFromCtx(r)
		sessions, err := st.GetCompanionSessions(u.ID)
		if err != nil {
			slog.Error("get companion sessions", "err", err)
			writeJSON(w, http.StatusInternalServerError, errResp("internal error"))
			return
		}
		type sessionResp struct {
			ID           int64  `json:"id"`
			Persona      string `json:"persona"`
			PersonaName  string `json:"persona_name"`
			Title        string `json:"title"`
			MsgCount     int    `json:"msg_count"`
			CreatedAt    string `json:"created_at"`
			UpdatedAt    string `json:"updated_at"`
		}
		out := make([]sessionResp, 0, len(sessions))
		for _, s := range sessions {
			p := GetPersona(s.Persona)
			out = append(out, sessionResp{
				ID:          s.ID,
				Persona:     s.Persona,
				PersonaName: p.Name,
				Title:       s.Title,
				MsgCount:    s.MsgCount,
				CreatedAt:   s.CreatedAt.Format("2006-01-02 15:04"),
				UpdatedAt:   s.UpdatedAt.Format("2006-01-02 15:04"),
			})
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// CreateSessionHandler handles POST /api/companion/sessions
func CreateSessionHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userFromCtx(r)
		var req struct {
			Persona string `json:"persona"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, errResp("invalid request"))
			return
		}
		if req.Persona != "male" && req.Persona != "female" && req.Persona != "huo" && req.Persona != "su" && req.Persona != "shen" && req.Persona != "pei" {
			req.Persona = "female"
		}
		p := GetPersona(req.Persona)
		id, err := st.CreateCompanionSession(u.ID, req.Persona, "新的对话")
		if err != nil {
			slog.Error("create companion session", "err", err)
			writeJSON(w, http.StatusInternalServerError, errResp("internal error"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":           id,
			"persona":      req.Persona,
			"persona_name": p.Name,
			"title":        "新的对话",
		})
	}
}

// DeleteSessionHandler handles DELETE /api/companion/sessions/{id}
func DeleteSessionHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userFromCtx(r)
		idStr := chi.URLParam(r, "id")
		sessionID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errResp("invalid id"))
			return
		}
		if err := st.DeleteCompanionSession(sessionID, u.ID); err != nil {
			slog.Error("delete companion session", "err", err)
			writeJSON(w, http.StatusInternalServerError, errResp("delete failed"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// MessagesHandler handles GET /api/companion/sessions/{id}/messages
func MessagesHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userFromCtx(r)
		idStr := chi.URLParam(r, "id")
		sessionID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errResp("invalid id"))
			return
		}
		// verify ownership
		sess, err := st.GetCompanionSession(sessionID, u.ID)
		if err != nil || sess == nil {
			writeJSON(w, http.StatusNotFound, errResp("session not found"))
			return
		}
		msgs, err := st.GetCompanionMessages(sessionID, u.ID, 200)
		if err != nil {
			slog.Error("get companion messages", "err", err)
			writeJSON(w, http.StatusInternalServerError, errResp("internal error"))
			return
		}
		type msgResp struct {
			ID        int64  `json:"id"`
			Role      string `json:"role"`
			Content   string `json:"content"`
			CreatedAt string `json:"created_at"`
		}
		out := make([]msgResp, 0, len(msgs))
		for _, m := range msgs {
			out = append(out, msgResp{
				ID:        m.ID,
				Role:      m.Role,
				Content:   m.Content,
				CreatedAt: m.CreatedAt.Format("15:04"),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"session": map[string]any{
				"id":           sess.ID,
				"persona":      sess.Persona,
				"persona_name": GetPersona(sess.Persona).Name,
				"title":        sess.Title,
				"msg_count":    sess.MsgCount,
			},
			"messages": out,
		})
	}
}

// ChatHandler handles POST /api/companion/chat
func ChatHandler(st *store.Store, client *api.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userFromCtx(r)
		var req struct {
			SessionID int64  `json:"session_id"`
			Content   string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, errResp("invalid request"))
			return
		}
		content := strings.TrimSpace(req.Content)
		if content == "" {
			writeJSON(w, http.StatusBadRequest, errResp("content is empty"))
			return
		}

		// Load or auto-create session
		var sess *store.CompanionSession
		if req.SessionID > 0 {
			var err error
			sess, err = st.GetCompanionSession(req.SessionID, u.ID)
			if err != nil || sess == nil {
				writeJSON(w, http.StatusNotFound, errResp("session not found"))
				return
			}
		} else {
			// Auto-create with default persona
			id, err := st.CreateCompanionSession(u.ID, "female", "新的对话")
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, errResp("create session failed"))
				return
			}
			sess, _ = st.GetCompanionSession(id, u.ID)
		}

		persona := GetPersona(sess.Persona)

		// Load memory
		rawMem, _ := st.GetCompanionMemoryRaw(u.ID)
		mem := parseMemory(rawMem)
		mem.TotalUserMessages++

		// Increment msg count
		_ = st.IncrCompanionMsgCount(sess.ID)

		// Save user message
		if err := st.SaveCompanionMessage(sess.ID, u.ID, "user", content); err != nil {
			slog.Error("save companion user message", "err", err)
			writeJSON(w, http.StatusInternalServerError, errResp("save failed"))
			return
		}

		// Load recent history for context
		history, _ := st.GetCompanionMessages(sess.ID, u.ID, MaxHistory)

// Build OpenRouter messages with dynamic persona + state + memory
		sysPrompt := buildSystemPrompt(persona, mem, time.Now())
		var chatMsgs []api.Message
		chatMsgs = append(chatMsgs, api.Message{Role: "system", Content: sysPrompt})
		for _, m := range history {
			if m.Role == "user" && m.Content == content && m.ID == history[len(history)-1].ID {
				chatMsgs = append(chatMsgs, api.Message{Role: "user", Content: content})
				break
			}
			chatMsgs = append(chatMsgs, api.Message{Role: m.Role, Content: m.Content})
		}
		if len(history) == 0 {
			chatMsgs = append(chatMsgs, api.Message{Role: "user", Content: content})
		}

		// Set SSE headers
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		flusher, canFlush := w.(http.Flusher)

		// Send session_id as first event so client knows the session
		sessJSON, _ := json.Marshal(map[string]any{"session_id": sess.ID})
		fmt.Fprintf(w, "event: meta\ndata: %s\n\n", sessJSON)
		if canFlush {
			flusher.Flush()
		}

		chatOpts := buildChatOptions(persona)
		reply, err := client.StreamChatCompletion(r.Context(), ModelChat, chatMsgs, chatOpts, func(rawData string) {
			fmt.Fprintf(w, "data: %s\n\n", rawData)
			if canFlush {
				flusher.Flush()
			}
		})

		if err != nil {
			slog.Warn("stream: primary model failed, falling back", "err", err)
			// Fallback: non-streaming
			reply, err = client.ChatCompletion(ModelFallback, chatMsgs, chatOpts)
			if err != nil {
				slog.Error("stream: fallback failed", "err", err)
				fmt.Fprintf(w, "event: error\ndata: {\"error\":\"AI 暂时不可用\"}\n\n")
				if canFlush {
					flusher.Flush()
				}
				return
			}
			// Send the full reply as a synthetic chunk
			synth, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{{"delta": map[string]any{"content": reply}}},
			})
			fmt.Fprintf(w, "data: %s\n\n", synth)
			if canFlush {
				flusher.Flush()
			}
		}

		fmt.Fprintf(w, "data: [DONE]\n\n")
		if canFlush {
			flusher.Flush()
		}

		// Persist and async tasks
		if err := st.SaveCompanionMessage(sess.ID, u.ID, "assistant", reply); err != nil {
			slog.Error("stream: save assistant message", "err", err)
		}
		_ = st.UpdateCompanionSessionTime(sess.ID)

		allMsgs, _ := st.GetCompanionMessages(sess.ID, u.ID, 20)
		userMsgCount := 0
		for _, m := range allMsgs {
			if m.Role == "user" {
				userMsgCount++
			}
		}
		if userMsgCount > 0 && userMsgCount%MemUpdateEvery == 0 {
			updateMemoryAsync(st, client, u.ID, allMsgs)
		} else {
			go func() {
				rawM, _ := st.GetCompanionMemoryRaw(u.ID)
				m := parseMemory(rawM)
				m.TotalUserMessages = userMsgCount
				_ = st.SaveCompanionMemory(u.ID, m)
			}()
		}
		if userMsgCount == TitleGenAt && sess.Title == "新的对话" {
			generateTitleAsync(st, client, sess.ID, allMsgs)
		}
	}
}

// StreamChatHandler handles POST /api/companion/chat/stream (SSE streaming).
func StreamChatHandler(st *store.Store, client *api.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userFromCtx(r)
		var req struct {
			SessionID int64  `json:"session_id"`
			Content   string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, errResp("invalid request"))
			return
		}
		content := strings.TrimSpace(req.Content)
		if content == "" {
			writeJSON(w, http.StatusBadRequest, errResp("content is empty"))
			return
		}

		var sess *store.CompanionSession
		if req.SessionID > 0 {
			var err error
			sess, err = st.GetCompanionSession(req.SessionID, u.ID)
			if err != nil || sess == nil {
				writeJSON(w, http.StatusNotFound, errResp("session not found"))
				return
			}
		} else {
			id, err := st.CreateCompanionSession(u.ID, "female", "新的对话")
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, errResp("create session failed"))
				return
			}
			sess, _ = st.GetCompanionSession(id, u.ID)
		}

		persona := GetPersona(sess.Persona)
		rawMem, _ := st.GetCompanionMemoryRaw(u.ID)
		mem := parseMemory(rawMem)
		mem.TotalUserMessages++
		mem.TurnCount = sess.MsgCount + 1
		_ = st.IncrCompanionMsgCount(sess.ID)

		if err := st.SaveCompanionMessage(sess.ID, u.ID, "user", content); err != nil {
			slog.Error("stream: save user message", "err", err)
			writeJSON(w, http.StatusInternalServerError, errResp("save failed"))
			return
		}

		history, _ := st.GetCompanionMessages(sess.ID, u.ID, MaxHistory)
		sysPrompt := buildSystemPrompt(persona, mem, time.Now())

		// Inject anti-repetition keyword list
		if kws := extractRecentKeywords(history); len(kws) > 0 {
			sysPrompt += fmt.Sprintf("\n\n[最近你自己的话里出现过这些词，回复里尽量避开：「%s」]", strings.Join(kws, "、"))
		}

		var chatMsgs []api.Message
		chatMsgs = append(chatMsgs, api.Message{Role: "system", Content: sysPrompt})
		for _, m := range history {
			if m.Role == "user" && m.Content == content && m.ID == history[len(history)-1].ID {
				chatMsgs = append(chatMsgs, api.Message{Role: "user", Content: content})
				break
			}
			chatMsgs = append(chatMsgs, api.Message{Role: m.Role, Content: m.Content})
		}
		if len(history) == 0 {
			chatMsgs = append(chatMsgs, api.Message{Role: "user", Content: content})
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		flusher, canFlush := w.(http.Flusher)

		sessJSON, _ := json.Marshal(map[string]any{"session_id": sess.ID})
		fmt.Fprintf(w, "event: meta\ndata: %s\n\n", sessJSON)
		if canFlush {
			flusher.Flush()
		}

		chatOpts := buildChatOptions(persona)
		reply, err := client.StreamChatCompletion(r.Context(), ModelChat, chatMsgs, chatOpts, func(rawData string) {
			fmt.Fprintf(w, "data: %s\n\n", rawData)
			if canFlush {
				flusher.Flush()
			}
		})

		if err != nil {
			slog.Warn("stream: primary model failed, falling back", "err", err)
			reply, err = client.ChatCompletion(ModelFallback, chatMsgs, chatOpts)
			if err != nil {
				slog.Error("stream: fallback failed", "err", err)
				fmt.Fprintf(w, "event: error\ndata: {\"error\":\"AI 暂时不可用\"}\n\n")
				if canFlush {
					flusher.Flush()
				}
				return
			}
			synth, _ := json.Marshal(map[string]any{
				"choices": []map[string]any{{"delta": map[string]any{"content": reply}}},
			})
			fmt.Fprintf(w, "data: %s\n\n", synth)
			if canFlush {
				flusher.Flush()
			}
		}

		fmt.Fprintf(w, "data: [DONE]\n\n")
		if canFlush {
			flusher.Flush()
		}

		if err := st.SaveCompanionMessage(sess.ID, u.ID, "assistant", reply); err != nil {
			slog.Error("stream: save assistant message", "err", err)
		}
		_ = st.UpdateCompanionSessionTime(sess.ID)

		allMsgs, _ := st.GetCompanionMessages(sess.ID, u.ID, 20)
		userMsgCount := 0
		for _, m := range allMsgs {
			if m.Role == "user" {
				userMsgCount++
			}
		}
		if userMsgCount > 0 && userMsgCount%MemUpdateEvery == 0 {
			updateMemoryAsync(st, client, u.ID, allMsgs)
		} else {
			go func() {
				rawM, _ := st.GetCompanionMemoryRaw(u.ID)
				m := parseMemory(rawM)
				m.TotalUserMessages = userMsgCount
				m.TurnCount = userMsgCount
				m.LastInteractionAt = time.Now().Format(time.RFC3339)
				_ = st.SaveCompanionMemory(u.ID, m)
			}()
		}
		if userMsgCount == TitleGenAt && sess.Title == "新的对话" {
			generateTitleAsync(st, client, sess.ID, allMsgs)
		}
	}
}

// MemoryHandler handles GET /api/companion/memory (debug endpoint)
func MemoryHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userFromCtx(r)
		raw, _ := st.GetCompanionMemoryRaw(u.ID)
		mem := parseMemory(raw)
		writeJSON(w, http.StatusOK, mem)
	}
}
