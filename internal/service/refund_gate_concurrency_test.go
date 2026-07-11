package service

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// dbCounter 为每个测试生成唯一的内存库名,避免 cache=shared 跨测试残留数据
var dbCounter int64

// userCounter 为每个 seed 的 user 生成唯一 username,避免 UNIQUE 约束冲突
var userCounter int64

// TestMain 初始化全局 logger,避免 service 层 logger.Warn/Error 触发 nil panic。
func TestMain(m *testing.M) {
	_ = logger.Init()
	os.Exit(m.Run())
}

// setupTestDB 建一个 SQLite 内存库并注入 model.DB(),用于验证 DB 原子语义。
// 注意: SQLite 与 MySQL 并发语义不完全一致(SQLite 写串行化),但
// "UPDATE ... WHERE cond" 的 RowsAffected 抢占语义是 SQL 标准行为,两者一致,
// 足以验证退款闸门 / 终态守卫的正确性。
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	// 每个测试独立命名的共享内存库,测试间互不干扰
	dsn := fmt.Sprintf("file:memtest%d?mode=memory&cache=shared", atomic.AddInt64(&dbCounter, 1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// SQLite 单写者,提高 busy_timeout 避免并发写立即报错
	db.Exec("PRAGMA busy_timeout = 5000")
	if err := db.AutoMigrate(&model.User{}, &model.Token{}, &model.Task{}, &model.BillingLog{}, &model.ChannelAccount{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	model.SetDB(db)
	return db
}

// seedTask 建一条处于 processing 状态、成本 cost 的任务及其 token/user/account。
func seedTask(t *testing.T, cost decimal.Decimal, initialBalance decimal.Decimal, currentTasks int) *model.Task {
	t.Helper()
	user := &model.User{Username: "u_" + GenerateTaskNo(), Balance: initialBalance}
	if err := model.DB().Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	token := &model.Token{UserID: user.ID, Balance: initialBalance, TotalUsed: cost}
	if err := model.DB().Create(token).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	acc := &model.ChannelAccount{CurrentTasks: currentTasks}
	if err := model.DB().Create(acc).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}
	task := &model.Task{
		TaskNo:    GenerateTaskNo(),
		UserID:    user.ID,
		TokenID:   token.ID,
		AccountID: acc.ID,
		Status:    model.TaskStatusProcessing,
		Cost:      cost,
	}
	if err := model.DB().Create(task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task
}

// TestUpdateTaskFail_ConcurrentOnlyOneCommits 验证: N 个 goroutine 并发对同一任务
// 调 UpdateTaskFail, 只有 1 个返回 committed=true (终态守卫抢占成功)。
func TestUpdateTaskFail_ConcurrentOnlyOneCommits(t *testing.T) {
	setupTestDB(t)
	task := seedTask(t, decimal.NewFromFloat(1.5), decimal.NewFromFloat(10), 1)

	svc := NewTaskService()
	const workers = 20
	var wg sync.WaitGroup
	var committedCount int64
	var mu sync.Mutex

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			committed, err := svc.UpdateTaskFail(task.ID, "concurrent fail")
			if err != nil {
				t.Errorf("UpdateTaskFail err: %v", err)
				return
			}
			if committed {
				mu.Lock()
				committedCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if committedCount != 1 {
		t.Fatalf("终态守卫失效: 期望恰好 1 次 committed, 实际 %d", committedCount)
	}

	// 验证退款只发生一次: token 余额 = 初始 10 + 退款 1.5 = 11.5
	var token model.Token
	model.DB().First(&token, task.TokenID)
	want := decimal.NewFromFloat(11.5)
	if !token.Balance.Equal(want) {
		t.Fatalf("重复退款: token 余额期望 %s, 实际 %s", want, token.Balance)
	}

	// 验证退款流水只有 1 条
	var refundLogs int64
	model.DB().Model(&model.BillingLog{}).
		Where("type = ? AND idempotent_key = ?", model.BillingTypeRefund, task.TaskNo).
		Count(&refundLogs)
	if refundLogs != 1 {
		t.Fatalf("退款流水应恰好 1 条, 实际 %d", refundLogs)
	}
}

// TestUpdateTaskSuccess_GuardedByTerminalState 验证: 任务已 failed 后,
// UpdateTaskSuccess 不能再把它改成 success (终态守卫)。
func TestUpdateTaskSuccess_GuardedByTerminalState(t *testing.T) {
	setupTestDB(t)
	task := seedTask(t, decimal.NewFromFloat(1), decimal.NewFromFloat(10), 1)
	svc := NewTaskService()

	// 先判失败
	committed, err := svc.UpdateTaskFail(task.ID, "failed first")
	if err != nil || !committed {
		t.Fatalf("首次 UpdateTaskFail 应成功流转, committed=%v err=%v", committed, err)
	}

	// 再尝试置成功 -> 应被守卫挡下
	committed2, err := svc.UpdateTaskSuccess(task.ID, map[string]any{"url": "x"}, decimal.NewFromFloat(1))
	if err != nil {
		t.Fatalf("UpdateTaskSuccess err: %v", err)
	}
	if committed2 {
		t.Fatalf("终态守卫失效: 已 failed 的任务被改成 success")
	}

	var got model.Task
	model.DB().First(&got, task.ID)
	if got.Status != model.TaskStatusFailed {
		t.Fatalf("任务状态应保持 failed, 实际 %s", got.Status)
	}
}

// TestCancelTask_RefundAndDecrementOnce 验证 CancelTask 抢占取消后退款一次、计数减一次;
// 且对已取消任务重复调用不再退款/递减。
func TestCancelTask_RefundAndDecrementOnce(t *testing.T) {
	setupTestDB(t)
	task := seedTask(t, decimal.NewFromFloat(2), decimal.NewFromFloat(10), 1)
	svc := NewTaskService()

	if err := svc.CancelTask(task.TaskNo, task.UserID); err != nil {
		t.Fatalf("首次取消应成功: %v", err)
	}
	// 重复取消应失败(已是终态)
	if err := svc.CancelTask(task.TaskNo, task.UserID); err == nil {
		t.Fatalf("重复取消应返回错误")
	}

	// 退款只发生一次: 10 + 2 = 12
	var token model.Token
	model.DB().First(&token, task.TokenID)
	want := decimal.NewFromFloat(12)
	if !token.Balance.Equal(want) {
		t.Fatalf("取消退款异常: 余额期望 %s, 实际 %s", want, token.Balance)
	}

	// 计数只减一次: 1 -> 0
	var acc model.ChannelAccount
	model.DB().First(&acc, task.AccountID)
	if acc.CurrentTasks != 0 {
		t.Fatalf("账号计数应减到 0, 实际 %d", acc.CurrentTasks)
	}
}

// TestConcurrentFail_DecrementOnce verifies that the terminal transaction
// releases the account slot exactly once under concurrent failure attempts.
func TestConcurrentFail_DecrementOnce(t *testing.T) {
	setupTestDB(t)
	// 账号初始 5 个在跑任务, 其中 1 个是本任务
	task := seedTask(t, decimal.NewFromFloat(1), decimal.NewFromFloat(10), 5)

	svc := NewTaskService()
	const workers = 20
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			_, _ = svc.UpdateTaskFail(task.ID, "concurrent")
		}()
	}
	wg.Wait()

	// 计数应恰好减 1: 5 -> 4 (只有抢到终态的那个 worker 递减)
	var acc model.ChannelAccount
	model.DB().First(&acc, task.AccountID)
	if acc.CurrentTasks != 4 {
		t.Fatalf("重复递减竞态: 账号计数期望 4 (只减一次), 实际 %d", acc.CurrentTasks)
	}

	// 退款也应只发生一次
	var token model.Token
	model.DB().First(&token, task.TokenID)
	want := decimal.NewFromFloat(11)
	if !token.Balance.Equal(want) {
		t.Fatalf("重复退款: 余额期望 %s, 实际 %s", want, token.Balance)
	}
}

func TestUpdateTaskFailRollsBackWhenRefundFails(t *testing.T) {
	setupTestDB(t)
	account := &model.ChannelAccount{CurrentTasks: 1}
	if err := model.DB().Create(account).Error; err != nil {
		t.Fatalf("create account: %v", err)
	}
	task := &model.Task{
		TaskNo:    GenerateTaskNo(),
		TokenID:   999,
		AccountID: account.ID,
		Status:    model.TaskStatusProcessing,
		Cost:      decimal.NewFromInt(1),
	}
	if err := model.DB().Create(task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}

	service := NewTaskService()
	committed, err := service.UpdateTaskFail(task.ID, "upstream failed")
	if err == nil || committed {
		t.Fatalf("first failure transition = committed %v, err %v; want refund error", committed, err)
	}

	var afterError model.Task
	if err := model.DB().First(&afterError, task.ID).Error; err != nil {
		t.Fatalf("reload task after refund error: %v", err)
	}
	if afterError.Status != model.TaskStatusProcessing || afterError.Refunded {
		t.Fatalf("task was committed despite refund error: status=%s refunded=%v",
			afterError.Status, afterError.Refunded)
	}
	var afterErrorAccount model.ChannelAccount
	if err := model.DB().First(&afterErrorAccount, account.ID).Error; err != nil {
		t.Fatalf("reload account after refund error: %v", err)
	}
	if afterErrorAccount.CurrentTasks != 1 {
		t.Fatalf("account slot changed despite rollback: %d", afterErrorAccount.CurrentTasks)
	}
	var refundLogs int64
	if err := model.DB().Model(&model.BillingLog{}).
		Where("idempotent_key = ?", task.TaskNo).Count(&refundLogs).Error; err != nil {
		t.Fatalf("count rolled back refund logs: %v", err)
	}
	if refundLogs != 0 {
		t.Fatalf("refund log count after rollback = %d, want 0", refundLogs)
	}

	token := &model.Token{
		BaseModel: model.BaseModel{ID: task.TokenID},
		Balance:   decimal.Zero,
		TotalUsed: task.Cost,
	}
	if err := model.DB().Create(token).Error; err != nil {
		t.Fatalf("create missing token: %v", err)
	}
	committed, err = service.UpdateTaskFail(task.ID, "upstream failed")
	if err != nil || !committed {
		t.Fatalf("retry failure transition = committed %v, err %v", committed, err)
	}

	var gotTask model.Task
	var gotToken model.Token
	var gotAccount model.ChannelAccount
	if err := model.DB().First(&gotTask, task.ID).Error; err != nil {
		t.Fatalf("reload failed task: %v", err)
	}
	if err := model.DB().First(&gotToken, token.ID).Error; err != nil {
		t.Fatalf("reload refunded token: %v", err)
	}
	if err := model.DB().First(&gotAccount, account.ID).Error; err != nil {
		t.Fatalf("reload released account: %v", err)
	}
	if gotTask.Status != model.TaskStatusFailed || !gotTask.Refunded ||
		!gotToken.Balance.Equal(task.Cost) || !gotToken.TotalUsed.IsZero() || gotAccount.CurrentTasks != 0 {
		t.Fatalf("retry did not commit refund: status=%s refunded=%v balance=%s total_used=%s current_tasks=%d",
			gotTask.Status, gotTask.Refunded, gotToken.Balance, gotToken.TotalUsed, gotAccount.CurrentTasks)
	}
}

func TestUpdateTaskFailPreservesDatabaseErrors(t *testing.T) {
	db := setupTestDB(t)
	task := &model.Task{TaskNo: GenerateTaskNo(), Status: model.TaskStatusProcessing}
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql db: %v", err)
	}

	_, err = NewTaskService().UpdateTaskFail(task.ID, "database failure")
	if err == nil {
		t.Fatal("expected database error")
	}
	if errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("database error was converted to ErrTaskNotFound: %v", err)
	}
}

func TestUpdateTaskFailRollsBackWhenAccountReleaseFails(t *testing.T) {
	db := setupTestDB(t)
	task := &model.Task{
		TaskNo:    GenerateTaskNo(),
		AccountID: 1,
		Status:    model.TaskStatusProcessing,
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := db.Migrator().DropTable(&model.ChannelAccount{}); err != nil {
		t.Fatalf("drop account table: %v", err)
	}

	committed, err := NewTaskService().UpdateTaskFail(task.ID, "upstream failure")
	if err == nil || committed {
		t.Fatalf("failure transition = committed %v, err %v; want account release error", committed, err)
	}
	var got model.Task
	if err := db.First(&got, task.ID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if got.Status != model.TaskStatusProcessing || got.CompletedAt != nil {
		t.Fatalf("task terminal state was not rolled back: status=%s completed_at=%v", got.Status, got.CompletedAt)
	}
}
