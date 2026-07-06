package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	"github.com/openshift-virtualization/kubevirt-storage-latency-exporter/pkg/config"
	"github.com/openshift-virtualization/kubevirt-storage-latency-exporter/pkg/device"
	bpf "github.com/openshift-virtualization/kubevirt-storage-latency-exporter/pkg/ebpf"
	"github.com/openshift-virtualization/kubevirt-storage-latency-exporter/pkg/qmp"
)

func main() {
	cfg := config.Parse()
	setupLogging(cfg.LogLevel)

	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	slog.Info("starting kubevirt-storage-latency-exporter",
		"node", cfg.NodeName,
		"qmp", cfg.EnableQMP,
		"ebpf", cfg.EnableEBPF,
	)

	log := slog.Default()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	stores := startInformers(ctx, cfg.NodeName, log)

	if cfg.EnableQMP {
		startQMP(ctx, cfg, stores.podStore, log)
	}

	if cfg.EnableEBPF {
		startEBPF(ctx, cfg, stores, log)
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})

	srv := &http.Server{Addr: cfg.ListenAddress, Handler: mux}

	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		srv.Shutdown(shutdownCtx)
	}()

	slog.Info("metrics server starting", "address", cfg.ListenAddress)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}

type informerStores struct {
	podStore   cache.Store
	pvcIndexer cache.Indexer
}

func startInformers(ctx context.Context, nodeName string, log *slog.Logger) informerStores {
	k8sCfg, err := rest.InClusterConfig()
	if err != nil {
		log.Error("building in-cluster config", "error", err)
		os.Exit(1)
	}

	cs, err := kubernetes.NewForConfig(k8sCfg)
	if err != nil {
		log.Error("creating clientset", "error", err)
		os.Exit(1)
	}

	podFactory := informers.NewSharedInformerFactoryWithOptions(cs, 0,
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.FieldSelector = fmt.Sprintf("spec.nodeName=%s", nodeName)
		}),
	)
	podStore := podFactory.Core().V1().Pods().Informer().GetStore()

	pvcFactory := informers.NewSharedInformerFactory(cs, 0)
	pvcInformer := pvcFactory.Core().V1().PersistentVolumeClaims().Informer()
	pvcInformer.AddIndexers(cache.Indexers{
		device.PVCByPVIndexName: device.PVCByPVIndexFunc,
	})
	pvcIndexer := pvcInformer.GetIndexer()

	podFactory.Start(ctx.Done())
	pvcFactory.Start(ctx.Done())
	podFactory.WaitForCacheSync(ctx.Done())
	pvcFactory.WaitForCacheSync(ctx.Done())

	log.Info("informers synced")
	return informerStores{podStore: podStore, pvcIndexer: pvcIndexer}
}

func startQMP(ctx context.Context, cfg *config.Config, podStore cache.Store, log *slog.Logger) {
	criClient, err := qmp.NewCRIClient(cfg.QMPCRISocket)
	if err != nil {
		log.Error("qmp: creating CRI client", "error", err)
		os.Exit(1)
	}

	collector := qmp.NewCollector(qmp.PollerConfig{
		NodeName:     cfg.NodeName,
		PollInterval: cfg.QMPPollInterval,
		BoundariesNs: cfg.BoundariesNs,
		QMPTimeout:   cfg.QMPTimeout,
		Concurrency:  cfg.QMPConcurrency,
		Namespaces:   config.ParseNamespaces(cfg.Namespaces),
		LabelFilter:  cfg.QMPLabelFilter,
	}, podStore, criClient, log)

	prometheus.MustRegister(collector)
	go collector.Run(ctx)

	log.Info("qmp: subsystem started")
}

func startEBPF(ctx context.Context, cfg *config.Config, stores informerStores, log *slog.Logger) {
	resolver := device.NewResolver(
		cfg.NodeName,
		cfg.EBPFProcPath,
		time.Duration(cfg.EBPFScanInterval)*time.Second,
		stores.podStore,
		stores.pvcIndexer,
		log,
	)
	go resolver.Run(ctx)

	programs, err := bpf.LoadAndAttach(
		cfg.EnableEBPFBlock, cfg.EnableEBPFNFS, cfg.EnableEBPFNFSKprobe,
		cfg.EBPFBlockMapSize, cfg.EBPFNFSMapSize, cfg.EBPFNFSKprobeMapSize,
		log,
	)
	if err != nil {
		log.Warn("ebpf: failed to load programs, eBPF monitoring disabled", "error", err)
		return
	}

	go programs.RetryFailed(ctx, 1*time.Minute, log)

	go func() {
		<-ctx.Done()
		programs.Close()
	}()

	collector := bpf.NewCollector(programs, resolver, cfg.NodeName, cfg.Boundaries, config.ParseNamespaces(cfg.Namespaces), log)
	prometheus.MustRegister(collector)

	log.Info("ebpf: subsystem started",
		"block", programs.BlockActive,
		"nfs", programs.NFSActive,
		"nfsKprobe", programs.NFSKprobeActive,
	)
}

func setupLogging(level string) {
	var l slog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: l})))
}
