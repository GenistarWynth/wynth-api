package main

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/router"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	_ "github.com/QuantumNous/new-api/setting/performance_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	_ "net/http/pprof"
)

//go:embed web/default/dist
var buildFS embed.FS

//go:embed web/default/dist/index.html
var indexPage []byte

//go:embed web/classic/dist
var classicBuildFS embed.FS

//go:embed web/classic/dist/index.html
var classicIndexPage []byte

const defaultServerShutdownTimeout = 120 * time.Second

type gracefulHTTPServer interface {
	ListenAndServe() error
	Shutdown(context.Context) error
	Close() error
}

type serverLifecycle struct {
	server      gracefulHTTPServer
	signals     <-chan os.Signal
	timeout     time.Duration
	started     func()
	stopWorkers func()
	workerDone  []<-chan struct{}
	flush       func() error
}

func normalizeServerShutdownTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultServerShutdownTimeout
	}
	return timeout
}

func configuredServerShutdownTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("SHUTDOWN_TIMEOUT_SECONDS"))
	seconds, err := strconv.ParseInt(raw, 10, 64)
	maxSeconds := int64(time.Duration(1<<63-1) / time.Second)
	if err != nil || seconds <= 0 || seconds > maxSeconds {
		return defaultServerShutdownTimeout
	}
	return time.Duration(seconds) * time.Second
}

func serverShutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

func newServerShutdownSignalChannel() chan os.Signal {
	return make(chan os.Signal, 1)
}

func (l serverLifecycle) run() error {
	serverErr := make(chan error, 1)
	serverStarted := make(chan struct{})
	go func() {
		close(serverStarted)
		serverErr <- l.server.ListenAndServe()
	}()
	<-serverStarted
	if l.started != nil {
		l.started()
	}

	select {
	case err := <-serverErr:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		return errors.Join(err, l.cleanup())
	case <-l.signals:
		return l.cleanup()
	}
}

func (l serverLifecycle) cleanup() error {
	if l.stopWorkers != nil {
		l.stopWorkers()
	}

	ctx, cancel := context.WithTimeout(context.Background(), normalizeServerShutdownTimeout(l.timeout))
	defer cancel()

	var cleanupErrors []error
	contextErrorRecorded := false
	appendCleanupError := func(err error) {
		if err == nil {
			return
		}
		cleanupErrors = append(cleanupErrors, err)
	}
	appendContextError := func(err error) {
		if err == nil {
			return
		}
		if contextErr := ctx.Err(); contextErr != nil && errors.Is(err, contextErr) {
			if contextErrorRecorded {
				return
			}
			contextErrorRecorded = true
		}
		appendCleanupError(err)
	}
	serverClosed := false
	forceClose := func() {
		if serverClosed {
			return
		}
		serverClosed = true
		if err := l.server.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			appendCleanupError(err)
		}
	}

	if err := l.server.Shutdown(ctx); err != nil {
		appendContextError(err)
		forceClose()
	}

drainWorkers:
	for _, done := range l.workerDone {
		if done == nil {
			continue
		}
		select {
		case <-done:
		case <-ctx.Done():
			appendContextError(ctx.Err())
			forceClose()
			break drainWorkers
		}
	}

	if l.flush != nil {
		if err := l.flush(); err != nil {
			appendCleanupError(err)
		}
	}
	return errors.Join(cleanupErrors...)
}

func main() {
	if err := runApplication(); err != nil {
		common.FatalLog(err.Error())
	}
}

func runApplication() (runErr error) {
	startTime := time.Now()

	err := InitResources()
	if err != nil {
		return fmt.Errorf("failed to initialize resources: %w", err)
	}

	common.SysLog("New API " + common.Version + " started")
	if os.Getenv("GIN_MODE") != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}
	if common.DebugEnabled {
		common.SysLog("running in debug mode")
	}

	defer func() {
		if closeErr := model.CloseDB(); closeErr != nil {
			runErr = errors.Join(runErr, fmt.Errorf("failed to close database: %w", closeErr))
		}
	}()

	if common.RedisEnabled {
		// for compatibility with old versions
		common.MemoryCacheEnabled = true
	}
	if common.MemoryCacheEnabled {
		common.SysLog("memory cache enabled")
		common.SysLog(fmt.Sprintf("sync frequency: %d seconds", common.SyncFrequency))

		// Add panic recovery and retry for InitChannelCache
		var initChannelCacheErr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					common.SysLog(fmt.Sprintf("InitChannelCache panic: %v, retrying once", r))
					// Retry once
					_, _, fixErr := model.FixAbility()
					if fixErr != nil {
						initChannelCacheErr = fmt.Errorf("InitChannelCache failed: %w", fixErr)
					}
				}
			}()
			model.InitChannelCache()
		}()
		if initChannelCacheErr != nil {
			return initChannelCacheErr
		}

		go model.SyncChannelCache(common.SyncFrequency)
	}

	// 热更新配置
	go model.SyncOptions(common.SyncFrequency)

	// 周期性重载授权策略，保证多节点/多 master 部署下权限变更能传播到每个实例
	go authz.StartPolicySync(common.SyncFrequency)

	// 数据看板
	go model.UpdateQuotaData()

	if os.Getenv("CHANNEL_UPDATE_FREQUENCY") != "" {
		frequency, err := strconv.Atoi(os.Getenv("CHANNEL_UPDATE_FREQUENCY"))
		if err != nil {
			return fmt.Errorf("failed to parse CHANNEL_UPDATE_FREQUENCY: %w", err)
		}
		go controller.AutomaticallyUpdateChannels(frequency)
	}

	// Codex credential auto-refresh check every 10 minutes, refresh when expires within 1 day
	service.StartCodexCredentialAutoRefreshTask()

	// Subscription quota reset task (daily/weekly/monthly/custom)
	service.StartSubscriptionQuotaResetTask()

	// Upstream source auto-sync task
	service.StartUpstreamSourceAutoSyncWorker()
	service.StartChannelAutoPriorityWorker()

	accountPoolWorkerCtx, stopAccountPoolWorkers := context.WithCancel(context.Background())
	accountPoolCapabilityWorkerDone := service.StartAccountPoolCapabilityAutoDetectWorker(accountPoolWorkerCtx)
	accountPoolProxyWorkerDone := service.StartAccountPoolProxyProber(accountPoolWorkerCtx, 0)
	accountPoolXAIQuotaWorkerDone := service.StartAccountPoolXAIQuotaProbeWorker(accountPoolWorkerCtx)
	accountPoolXAIOAuthReconcileWorkerDone := service.StartAccountPoolXAIOAuthReconcileWorker(accountPoolWorkerCtx)

	// Per-channel monitor batch: master-only, sync.Once-guarded ticker that every
	// minute probes channels whose per-channel monitor is due and records their
	// availability history for the Channel Monitor dialog. This is the ONLY writer
	// of channel monitor logs. Keep this boot call — an upstream merge
	// (system-task-runner refactor, #5680) previously dropped it and silently
	// orphaned the monitor batch, so the dialog showed "no data" for every channel.
	go controller.AutomaticallyTestChannels()

	// Report this process as a system instance so the System Info page can show
	// all currently alive nodes in multi-instance deployments.
	service.StartSystemInstanceReporter()

	// Wire task polling adaptor factory (breaks service -> relay import cycle).
	// Must run before the system task runner starts: the async_task_poll handler
	// calls service.RunTaskPollingOnce, which needs this factory set.
	service.GetTaskAdaptorFunc = func(platform constant.TaskPlatform) service.TaskPollingAdaptor {
		a := relay.GetTaskAdaptor(platform)
		if a == nil {
			return nil
		}
		return a
	}

	// Register the periodic channel test, upstream model update, and async task
	// polling (Midjourney / Suno / video) jobs as scheduled system tasks
	// (DB-lease dedup across masters + run history), then start the runner that
	// schedules and executes them. Master-only execution and the UpdateTask
	// switch are enforced inside the runner and each handler's Enabled().
	controller.RegisterScheduledSystemTasks()
	service.StartSystemTaskRunner()

	if os.Getenv("BATCH_UPDATE_ENABLED") == "true" {
		common.BatchUpdateEnabled = true
		common.SysLog("batch update enabled with interval " + strconv.Itoa(common.BatchUpdateInterval) + "s")
		model.InitBatchUpdater()
	}

	if os.Getenv("ENABLE_PPROF") == "true" {
		gopool.Go(func() {
			log.Println(http.ListenAndServe("0.0.0.0:8005", nil))
		})
		go common.Monitor()
		common.SysLog("pprof enabled")
	}

	err = common.StartPyroScope()
	if err != nil {
		common.SysError(fmt.Sprintf("start pyroscope error : %v", err))
	}

	// Initialize HTTP server
	server := gin.New()
	server.Use(gin.CustomRecovery(func(c *gin.Context, err any) {
		common.SysLog(fmt.Sprintf("panic detected: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("Panic detected, error: %v. Please submit a issue here: https://github.com/Calcium-Ion/new-api", err),
				"type":    "new_api_panic",
			},
		})
	}))
	// This will cause SSE not to work!!!
	//server.Use(gzip.Gzip(gzip.DefaultCompression))
	server.Use(middleware.RequestId())
	server.Use(middleware.Version())
	server.Use(middleware.I18n())
	middleware.SetUpLogger(server)
	// Initialize session store
	store := cookie.NewStore([]byte(common.SessionSecret))
	store.Options(sessions.Options{
		Path:     "/",
		MaxAge:   2592000, // 30 days
		HttpOnly: true,
		Secure:   common.SessionCookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
	server.Use(sessions.Sessions("session", store))

	InjectUmamiAnalytics()
	InjectGoogleAnalytics()

	// 设置路由
	router.SetRouter(server, router.ThemeAssets{
		DefaultBuildFS:   buildFS,
		DefaultIndexPage: indexPage,
		ClassicBuildFS:   classicBuildFS,
		ClassicIndexPage: classicIndexPage,
	})
	var port = os.Getenv("PORT")
	if port == "" {
		port = strconv.Itoa(*common.Port)
	}

	httpServer := &http.Server{
		Addr:    ":" + port,
		Handler: server,
	}
	shutdownSignals := newServerShutdownSignalChannel()
	signal.Notify(shutdownSignals, serverShutdownSignals()...)
	defer signal.Stop(shutdownSignals)

	err = (serverLifecycle{
		server:  httpServer,
		signals: shutdownSignals,
		timeout: configuredServerShutdownTimeout(),
		started: func() {
			common.LogStartupSuccess(startTime, port)
		},
		stopWorkers: stopAccountPoolWorkers,
		workerDone: []<-chan struct{}{
			accountPoolCapabilityWorkerDone,
			accountPoolProxyWorkerDone,
			accountPoolXAIQuotaWorkerDone,
			accountPoolXAIOAuthReconcileWorkerDone,
		},
		flush: func() error {
			if common.DataExportEnabled {
				model.SaveQuotaDataCache()
			}
			return nil
		},
	}).run()
	if err != nil {
		return fmt.Errorf("HTTP server lifecycle failed: %w", err)
	}
	return nil
}

func InjectUmamiAnalytics() {
	analyticsInjectBuilder := &strings.Builder{}
	if os.Getenv("UMAMI_WEBSITE_ID") != "" {
		umamiSiteID := os.Getenv("UMAMI_WEBSITE_ID")
		umamiScriptURL := os.Getenv("UMAMI_SCRIPT_URL")
		if umamiScriptURL == "" {
			umamiScriptURL = "https://analytics.umami.is/script.js"
		}
		analyticsInjectBuilder.WriteString("<script defer src=\"")
		analyticsInjectBuilder.WriteString(umamiScriptURL)
		analyticsInjectBuilder.WriteString("\" data-website-id=\"")
		analyticsInjectBuilder.WriteString(umamiSiteID)
		analyticsInjectBuilder.WriteString("\"></script>")
	}
	analyticsInjectBuilder.WriteString("<!--Umami QuantumNous-->\n")
	analyticsInject := []byte(analyticsInjectBuilder.String())
	placeholder := []byte("<!--umami-->\n")
	indexPage = bytes.ReplaceAll(indexPage, placeholder, analyticsInject)
	classicIndexPage = bytes.ReplaceAll(classicIndexPage, placeholder, analyticsInject)
}

func InjectGoogleAnalytics() {
	analyticsInjectBuilder := &strings.Builder{}
	if os.Getenv("GOOGLE_ANALYTICS_ID") != "" {
		gaID := os.Getenv("GOOGLE_ANALYTICS_ID")
		// Google Analytics 4 (gtag.js)
		analyticsInjectBuilder.WriteString("<script async src=\"https://www.googletagmanager.com/gtag/js?id=")
		analyticsInjectBuilder.WriteString(gaID)
		analyticsInjectBuilder.WriteString("\"></script>")
		analyticsInjectBuilder.WriteString("<script>")
		analyticsInjectBuilder.WriteString("window.dataLayer = window.dataLayer || [];")
		analyticsInjectBuilder.WriteString("function gtag(){dataLayer.push(arguments);}")
		analyticsInjectBuilder.WriteString("gtag('js', new Date());")
		analyticsInjectBuilder.WriteString("gtag('config', '")
		analyticsInjectBuilder.WriteString(gaID)
		analyticsInjectBuilder.WriteString("');")
		analyticsInjectBuilder.WriteString("</script>")
	}
	analyticsInjectBuilder.WriteString("<!--Google Analytics QuantumNous-->\n")
	analyticsInject := []byte(analyticsInjectBuilder.String())
	placeholder := []byte("<!--Google Analytics-->\n")
	indexPage = bytes.ReplaceAll(indexPage, placeholder, analyticsInject)
	classicIndexPage = bytes.ReplaceAll(classicIndexPage, placeholder, analyticsInject)
}

func InitResources() error {
	// Initialize resources here if needed
	// This is a placeholder function for future resource initialization
	err := godotenv.Load(".env")
	if err != nil {
		if common.DebugEnabled {
			common.SysLog("No .env file found, using default environment variables. If needed, please create a .env file and set the relevant variables.")
		}
	}

	// 加载环境变量
	common.InitEnv()

	logger.SetupLogger()

	// Initialize model settings
	ratio_setting.InitRatioSettings()

	service.InitHttpClient()

	service.InitTokenEncoders()

	// Initialize SQL Database
	err = model.InitDB()
	if err != nil {
		common.FatalLog("failed to initialize database: " + err.Error())
		return err
	}
	if err = authz.Init(model.DB); err != nil {
		common.FatalLog("failed to initialize authorization: " + err.Error())
		return err
	}

	model.CheckSetup()

	// Initialize options, should after model.InitDB()
	model.InitOptionMap()

	// 清理旧的磁盘缓存文件
	common.CleanupOldCacheFiles()

	// 初始化模型
	model.GetPricing()

	// Initialize SQL Database
	err = model.InitLogDB()
	if err != nil {
		return err
	}

	// Initialize Redis
	err = common.InitRedisClient()
	if err != nil {
		return err
	}

	perfmetrics.Init()

	// 启动系统监控
	common.StartSystemMonitor()

	// Initialize i18n
	err = i18n.Init()
	if err != nil {
		common.SysError("failed to initialize i18n: " + err.Error())
		// Don't return error, i18n is not critical
	} else {
		common.SysLog("i18n initialized with languages: " + strings.Join(i18n.SupportedLanguages(), ", "))
	}
	// Register user language loader for lazy loading
	i18n.SetUserLangLoader(model.GetUserLanguage)

	// Load custom OAuth providers from database
	err = oauth.LoadCustomProviders()
	if err != nil {
		common.SysError("failed to load custom OAuth providers: " + err.Error())
		// Don't return error, custom OAuth is not critical
	}

	return nil
}
