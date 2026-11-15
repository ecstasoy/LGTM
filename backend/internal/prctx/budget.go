package prctx

// BudgetReport 记录 token 分配与被丢弃的内容；透传到 API 响应，便于前端标注压缩重。
type BudgetReport struct {
	TokenLimit int
	UsedL1     int
	UsedL2     int
	UsedL3     int
	UsedL4     int
	Dropped    []string // 被丢弃全文的文件路径
	Excluded   []string // lockfiles and generated files, skipped regardless of budget
	Clipped    []string // files whose diff was cut to fit; only the hunks shown were reviewed
}

// 默认比例 L1:L2:L3 = 4:5:1；超限压缩顺序 L3 → L2 → L1。
// 引入 L4 后改 3:4:1:2，压缩顺序 L3 → L4 → L2 → L1。
