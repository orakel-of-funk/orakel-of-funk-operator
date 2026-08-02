package orakel

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLogOrakel_LoadBaseline(t *testing.T) {
	for _, normalize := range []bool{false, true} {
		suffix := ""
		if normalize {
			suffix = "/normalizeTimestamps"
		}

		t.Run("LoadBaseline with empty input"+suffix, func(t *testing.T) {
			dm := NewLogOrakelWithNormalization(normalize)
			logCount, templateCount := dm.LoadBaseline([]string{})
			assert.Equal(t, 0, logCount, "Expected baseline logs count to be 0")
			assert.Equal(t, 0, templateCount, "Expected baseline logs count to be 0")
		})

		t.Run("LoadBaseline with valid input"+suffix, func(t *testing.T) {
			dm := NewLogOrakelWithNormalization(normalize)
			input := []string{"log line 1", "log line 2", "log line 3"}
			logCount, templateCount := dm.LoadBaseline(input)
			assert.Equal(t, 3, logCount, "Expected baseline logs count to be 3")
			assert.Equal(t, templateCount, 1, "Expected logs to be clustered into a single template")
		})

		t.Run("LoadBaseline with duplicate lines"+suffix, func(t *testing.T) {
			dm := NewLogOrakelWithNormalization(normalize)
			input := []string{"log line 1", "log line 1", "log line 2"}
			logCount, templateCount := dm.LoadBaseline(input)
			assert.Equal(t, 3, logCount, "Expected baseline logs count to be 3")
			assert.Equal(t, templateCount, 1, "Expected logs to be clustered into a single template")
		})
	}
}

func TestLogOrakel_AnalyzeLogs(t *testing.T) {
	for _, normalize := range []bool{false, true} {
		suffix := ""
		if normalize {
			suffix = "/normalizeTimestamps"
		}

		t.Run("AnalyzeLogs with empty input"+suffix, func(t *testing.T) {
			dm := NewLogOrakelWithNormalization(normalize)
			result, count := dm.AnalyzeTarget([]string{})
			assert.Equal(t, 0, len(result), "Expected no anomalies for empty input")
			assert.Equal(t, 0, count, "Excpected target line count to be 0")
		})

		t.Run("AnalyzeLogs with valid input"+suffix, func(t *testing.T) {
			dm := NewLogOrakelWithNormalization(normalize)
			input := []string{"log line 1", "log line 2", "log line 3"}
			result, count := dm.AnalyzeTarget(input)
			assert.Equal(t, 3, len(result), "Expected no anomalies for baseline logs")
			assert.Equal(t, len(input), count, "Expected target line count to be 3")
		})

		t.Run("AnalyzeLogs without anomalies but matches"+suffix, func(t *testing.T) {
			dm := NewLogOrakelWithNormalization(normalize)
			dm.LoadBaseline([]string{"log line 1", "log line 2"})
			input := []string{"log line 5", "log line 6"}
			result, count := dm.AnalyzeTarget(input)
			assert.Equal(t, 0, len(result), "Expected one anomaly detected")
			assert.Equal(t, len(input), count, "Expected target line count to be 2")
		})

		t.Run("AnalyzeLogs with empty baseline"+suffix, func(t *testing.T) {
			dm := NewLogOrakelWithNormalization(normalize)
			baselineLogs := []string{
				"ts=2025-07-07T13:59:39.37824263Z level=info caller=/workspace/cmd/prometheus-config-reloader/main.go:148 msg=\"Starting prometheus-config-reloader\"",
				"ts=2025-07-07T13:59:39.378453637Z level=info caller=/workspace/internal/goruntime/cpu.go:27 msg=\"Leaving GOMAXPROCS=12: CPU quota undefined\"",
				"level=info ts=2025-07-07T13:59:39.379052191Z caller=reloader.go:282 msg=\"reloading via HTTP\"",
				"ts=2025-07-07T13:59:39.379080425Z level=info caller=/workspace/cmd/prometheus-config-reloader/main.go:202 msg=\"Starting web server for metrics\" listen=0.0.0.0:8080",
				"level=info ts=2025-07-07T13:59:39.379151601Z caller=reloader.go:330 msg=\"started watching config file and directories for changes\" cfg= cfgDirs= out= dirs=/etc/config",
				"ts=2025-07-07T13:59:39.381626994Z level=info caller=/go/pkg/mod/github.com/prometheus/exporter-toolkit@v0.14.0/web/tls_config.go:347 msg=\"Listening on\" address=[::]:8080",
				"ts=2025-07-07T13:59:39.38166146Z level=info caller=/go/pkg/mod/github.com/prometheus/exporter-toolkit@v0.14.0/web/tls_config.go:350 msg=\"TLS is disabled.\" http2=false address=[::]:8080",
			}
			logCount, templateCount := dm.LoadBaseline([]string{})
			assert.Equal(t, 0, logCount, "Expected baseline logs count to be 0")
			assert.Equal(t, 0, templateCount, "Expected baseline logs count to be 0")

			result, count := dm.AnalyzeTarget(baselineLogs)
			assert.Equal(t, len(baselineLogs), len(result), "Expected all logs to be anomalies")
			assert.Equal(t, len(baselineLogs), count, "Expected all lines to be analyzed")
		})

		t.Run("AnalyzeLogs with identical baseline/target"+suffix, func(t *testing.T) {
			dm := NewLogOrakelWithNormalization(normalize)
			baselineLogs := []string{
				"ts=2025-07-07T13:59:39.37824263Z level=info caller=/workspace/cmd/prometheus-config-reloader/main.go:148 msg=\"Starting prometheus-config-reloader\"",
				"ts=2025-07-07T13:59:39.378453637Z level=info caller=/workspace/internal/goruntime/cpu.go:27 msg=\"Leaving GOMAXPROCS=12: CPU quota undefined\"",
				"level=info ts=2025-07-07T13:59:39.379052191Z caller=reloader.go:282 msg=\"reloading via HTTP\"",
				"ts=2025-07-07T13:59:39.379080425Z level=info caller=/workspace/cmd/prometheus-config-reloader/main.go:202 msg=\"Starting web server for metrics\" listen=0.0.0.0:8080",
				"level=info ts=2025-07-07T13:59:39.379151601Z caller=reloader.go:330 msg=\"started watching config file and directories for changes\" cfg= cfgDirs= out= dirs=/etc/config",
				"ts=2025-07-07T13:59:39.381626994Z level=info caller=/go/pkg/mod/github.com/prometheus/exporter-toolkit@v0.14.0/web/tls_config.go:347 msg=\"Listening on\" address=[::]:8080",
				"ts=2025-07-07T13:59:39.38166146Z level=info caller=/go/pkg/mod/github.com/prometheus/exporter-toolkit@v0.14.0/web/tls_config.go:350 msg=\"TLS is disabled.\" http2=false address=[::]:8080",
			}
			dm.LoadBaseline(baselineLogs)
			result, count := dm.AnalyzeTarget(baselineLogs)
			assert.Equal(t, 0, len(result), "Expected no anomalies")
			assert.Equal(t, len(baselineLogs), count, "Expected all lines to be analyzed")
		})

		t.Run("AnalyzeLogs with anomalies"+suffix, func(t *testing.T) {
			baselineLogs := []string{
				"time=2025-07-07T09:00:01.543Z level=INFO source=main.go:725 msg=\"Starting Prometheus Server\" mode=server version=\"(version=3.4.2, branch=HEAD, revision=b392caf256d7ed36980992496c8a6274e5557d36)\"",
				"time=2025-07-07T09:00:01.543Z level=INFO source=main.go:730 msg=\"operational information\" build_context=\"(go=go1.24.4, platform=linux/amd64, user=root@f841ca3a6e68, date=20250626-20:51:48, tags=netgo,builtinassets,stringlabels)\" host_details=\"(Linux 6.6.87.1-microsoft-standard-WSL2 #1 SMP PREEMPT_DYNAMIC Mon Apr 21 17:08:54 UTC 2025 x86_64 prometheus-server-b48bbcb5c-7crcg localdomain)\" fd_limits=\"(soft=1048576, hard=1048576)\" vm_limits=\"(soft=unlimited, hard=unlimited)\"",
				"time=2025-07-07T09:00:01.544Z level=INFO source=main.go:806 msg=\"Leaving GOMAXPROCS=12: CPU quota undefined\" component=automaxprocs",
				"time=2025-07-07T09:00:01.545Z level=INFO source=web.go:656 msg=\"Start listening for connections\" component=web address=0.0.0.0:9090",
				"time=2025-07-07T09:00:01.545Z level=INFO source=main.go:1266 msg=\"Starting TSDB ...\"",
				"time=2025-07-07T09:00:01.547Z level=INFO source=tls_config.go:347 msg=\"Listening on\" component=web address=[::]:9090",
				"time=2025-07-07T09:00:01.547Z level=INFO source=tls_config.go:350 msg=\"TLS is disabled.\" component=web http2=false address=[::]:9090",
				"time=2025-07-07T09:00:01.549Z level=INFO source=head.go:657 msg=\"Replaying on-disk memory mappable chunks if any\" component=tsdb",
				"time=2025-07-07T09:00:01.549Z level=INFO source=head.go:744 msg=\"On-disk memory mappable chunks replay completed\" component=tsdb duration=610ns",
				"time=2025-07-07T09:00:01.549Z level=INFO source=head.go:752 msg=\"Replaying WAL, this may take a while\" component=tsdb",
				"time=2025-07-07T09:00:01.549Z level=INFO source=head.go:825 msg=\"WAL segment loaded\" component=tsdb segment=0 maxSegment=0 duration=278.751µs",
				"time=2025-07-07T09:00:01.549Z level=INFO source=head.go:862 msg=\"WAL replay completed\" component=tsdb checkpoint_replay_duration=25.562µs wal_replay_duration=302.724µs wbl_replay_duration=130ns chunk_snapshot_load_duration=0s mmap_chunk_replay_duration=610ns total_replay_duration=367.681µs",
				"time=2025-07-07T09:00:01.551Z level=INFO source=main.go:1287 msg=\"filesystem information\" fs_type=EXT4_SUPER_MAGIC",
				"time=2025-07-07T09:00:01.551Z level=INFO source=main.go:1290 msg=\"TSDB started\"",
				"time=2025-07-07T09:00:01.551Z level=INFO source=main.go:1475 msg=\"Loading configuration file\" filename=/etc/config/prometheus.yml",
				"time=2025-07-07T09:00:01.552Z level=INFO source=kubernetes.go:321 msg=\"Using pod service account via in-cluster config\" component=\"discovery manager scrape\" discovery=kubernetes config=prometheus-pushgateway",
				"time=2025-07-07T09:00:01.552Z level=INFO source=kubernetes.go:321 msg=\"Using pod service account via in-cluster config\" component=\"discovery manager scrape\" discovery=kubernetes config=kubernetes-pods-slow",
				"time=2025-07-07T09:00:01.553Z level=INFO source=kubernetes.go:321 msg=\"Using pod service account via in-cluster config\" component=\"discovery manager scrape\" discovery=kubernetes config=kubernetes-apiservers",
				"time=2025-07-07T09:00:01.553Z level=INFO source=kubernetes.go:321 msg=\"Using pod service account via in-cluster config\" component=\"discovery manager scrape\" discovery=kubernetes config=kubernetes-nodes",
				"time=2025-07-07T09:00:01.553Z level=INFO source=kubernetes.go:321 msg=\"Using pod service account via in-cluster config\" component=\"discovery manager notify\" discovery=kubernetes config=config-0",
				"time=2025-07-07T09:00:01.554Z level=INFO source=main.go:1514 msg=\"updated GOGC\" old=100 new=75",
				"time=2025-07-07T09:00:01.554Z level=INFO source=main.go:1524 msg=\"Completed loading of configuration file\" db_storage=620ns remote_storage=810ns web_handler=140ns query_engine=5.421µs scrape=165.708µs scrape_sd=951.654µs notify=284.611µs notify_sd=264.239µs rules=178.149µs tracing=2.52µs filename=/etc/config/prometheus.yml totalDuration=3.014228ms",
				"time=2025-07-07T09:00:01.554Z level=INFO source=main.go:1251 msg=\"Server is ready to receive web requests.\"",
				"time=2025-07-07T09:00:01.554Z level=INFO source=manager.go:175 msg=\"Starting rule manager...\" component=\"rule manager\"",
				"time=2025-07-07T09:00:01.559Z level=INFO source=warnings.go:70 msg=\"v1 Endpoints is deprecated in v1.33+; use discovery.k8s.io/v1 EndpointSlice\" component=k8s_client_runtime",
				"time=2025-07-07T09:00:01.560Z level=INFO source=warnings.go:70 msg=\"v1 Endpoints is deprecated in v1.33+; use discovery.k8s.io/v1 EndpointSlice\" component=k8s_client_runtime",
				"time=2025-07-07T09:02:55.223Z level=INFO source=main.go:1475 msg=\"Loading configuration file\" filename=/etc/config/prometheus.yml",
				"time=2025-07-07T09:02:55.224Z level=INFO source=main.go:1524 msg=\"Completed loading of configuration file\" db_storage=1.45µs remote_storage=1.09µs web_handler=370ns query_engine=910ns scrape=467.536µs scrape_sd=34.21µs notify=197.723µs notify_sd=7.44µs rules=93.761µs tracing=2.34µs filename=/etc/config/prometheus.yml totalDuration=1.754081ms",
				"time=2025-07-07T09:05:34.562Z level=INFO source=warnings.go:70 msg=\"v1 Endpoints is deprecated in v1.33+; use discovery.k8s.io/v1 EndpointSlice\" component=k8s_client_runtime",
				"time=2025-07-07T09:13:21.564Z level=INFO source=warnings.go:70 msg=\"v1 Endpoints is deprecated in v1.33+; use discovery.k8s.io/v1 EndpointSlice\" component=k8s_client_runtime",
			}

			baselineLogs = append(baselineLogs, []string{
				"time=2025-07-07T14:49:19.195Z level=INFO source=main.go:725 msg=\"Starting Prometheus Server\" mode=server version=\"(version=3.4.2, branch=HEAD, revision=b392caf256d7ed36980992496c8a6274e5557d36)\"",
				"time=2025-07-07T14:49:19.195Z level=INFO source=main.go:730 msg=\"operational information\" build_context=\"(go=go1.24.4, platform=linux/amd64, user=root@f841ca3a6e68, date=20250626-20:51:48, tags=netgo,builtinassets,stringlabels)\" host_details=\"(Linux 6.6.87.1-microsoft-standard-WSL2 #1 SMP PREEMPT_DYNAMIC Mon Apr 21 17:08:54 UTC 2025 x86_64 prometheus-server-b48bbcb5c-lp2cs localdomain)\" fd_limits=\"(soft=1048576, hard=1048576)\" vm_limits=\"(soft=unlimited, hard=unlimited)\"",
				"time=2025-07-07T14:49:19.196Z level=INFO source=main.go:806 msg=\"Leaving GOMAXPROCS=12: CPU quota undefined\" component=automaxprocs",
				"time=2025-07-07T14:49:19.197Z level=INFO source=web.go:656 msg=\"Start listening for connections\" component=web address=0.0.0.0:9090",
				"time=2025-07-07T14:49:19.198Z level=INFO source=main.go:1266 msg=\"Starting TSDB ...\"",
				"time=2025-07-07T14:49:19.199Z level=INFO source=repair.go:54 msg=\"Found healthy block\" component=tsdb mint=1751878807354 maxt=1751882400000 ulid=01JZJC41D1J0FQ5D5Z4M3J6YFX",
				"time=2025-07-07T14:49:19.199Z level=INFO source=repair.go:54 msg=\"Found healthy block\" component=tsdb mint=1751882402302 maxt=1751889600000 ulid=01JZJFHT0FDWZ4A37R929T6VMS",
				"time=2025-07-07T14:49:19.200Z level=INFO source=tls_config.go:347 msg=\"Listening on\" component=web address=[::]:9090",
				"time=2025-07-07T14:49:19.200Z level=INFO source=tls_config.go:350 msg=\"TLS is disabled.\" component=web http2=false address=[::]:9090",
				"time=2025-07-07T14:49:19.205Z level=INFO source=head.go:657 msg=\"Replaying on-disk memory mappable chunks if any\" component=tsdb",
				"time=2025-07-07T14:49:19.259Z level=INFO source=head.go:744 msg=\"On-disk memory mappable chunks replay completed\" component=tsdb duration=53.852389ms",
				"time=2025-07-07T14:49:19.259Z level=INFO source=head.go:752 msg=\"Replaying WAL, this may take a while\" component=tsdb",
				"time=2025-07-07T14:49:19.609Z level=INFO source=head.go:825 msg=\"WAL segment loaded\" component=tsdb segment=0 maxSegment=3 duration=350.334485ms",
				"time=2025-07-07T14:49:19.727Z level=INFO source=head.go:825 msg=\"WAL segment loaded\" component=tsdb segment=1 maxSegment=3 duration=117.676689ms",
				"time=2025-07-07T14:49:19.857Z level=INFO source=head.go:825 msg=\"WAL segment loaded\" component=tsdb segment=2 maxSegment=3 duration=130.215557ms",
				"time=2025-07-07T14:49:19.858Z level=INFO source=head.go:825 msg=\"WAL segment loaded\" component=tsdb segment=3 maxSegment=3 duration=242.899µs",
				"time=2025-07-07T14:49:19.858Z level=INFO source=head.go:862 msg=\"WAL replay completed\" component=tsdb checkpoint_replay_duration=69.901µs wal_replay_duration=598.662516ms wbl_replay_duration=133ns chunk_snapshot_load_duration=0s mmap_chunk_replay_duration=53.852389ms total_replay_duration=652.641463ms",
				"time=2025-07-07T14:49:19.911Z level=INFO source=main.go:1287 msg=\"filesystem information\" fs_type=EXT4_SUPER_MAGIC",
				"time=2025-07-07T14:49:19.911Z level=INFO source=main.go:1290 msg=\"TSDB started\"",
				"time=2025-07-07T14:49:19.911Z level=INFO source=main.go:1475 msg=\"Loading configuration file\" filename=/etc/config/prometheus.yml",
				"time=2025-07-07T14:49:19.913Z level=INFO source=kubernetes.go:321 msg=\"Using pod service account via in-cluster config\" component=\"discovery manager scrape\" discovery=kubernetes config=kubernetes-pods",
				"time=2025-07-07T14:49:19.913Z level=INFO source=kubernetes.go:321 msg=\"Using pod service account via in-cluster config\" component=\"discovery manager scrape\" discovery=kubernetes config=kubernetes-service-endpoints",
				"time=2025-07-07T14:49:19.913Z level=INFO source=kubernetes.go:321 msg=\"Using pod service account via in-cluster config\" component=\"discovery manager scrape\" discovery=kubernetes config=kubernetes-services",
				"time=2025-07-07T14:49:19.913Z level=INFO source=kubernetes.go:321 msg=\"Using pod service account via in-cluster config\" component=\"discovery manager scrape\" discovery=kubernetes config=kubernetes-nodes",
				"time=2025-07-07T14:49:19.914Z level=INFO source=kubernetes.go:321 msg=\"Using pod service account via in-cluster config\" component=\"discovery manager notify\" discovery=kubernetes config=config-0",
				"time=2025-07-07T14:49:19.914Z level=INFO source=main.go:1514 msg=\"updated GOGC\" old=100 new=75",
				"time=2025-07-07T14:49:19.914Z level=INFO source=main.go:1524 msg=\"Completed loading of configuration file\" db_storage=1.011µs remote_storage=1.348µs web_handler=470ns query_engine=8.588µs scrape=290.748µs scrape_sd=1.094836ms notify=177.128µs notify_sd=212.289µs rules=159.523µs tracing=3.533µs filename=/etc/config/prometheus.yml totalDuration=3.115508ms",
				"time=2025-07-07T14:49:19.914Z level=INFO source=main.go:1251 msg=\"Server is ready to receive web requests.\"",
				"time=2025-07-07T14:49:19.914Z level=INFO source=manager.go:175 msg=\"Starting rule manager...\" component=\"rule manager\"",
				"time=2025-07-07T14:49:19.917Z level=INFO source=warnings.go:70 msg=\"v1 Endpoints is deprecated in v1.33+; use discovery.k8s.io/v1 EndpointSlice\" component=k8s_client_runtime",
				"time=2025-07-07T14:49:19.918Z level=INFO source=warnings.go:70 msg=\"v1 Endpoints is deprecated in v1.33+; use discovery.k8s.io/v1 EndpointSlice\" component=k8s_client_runtime",
			}...)

			dm := NewLogOrakelWithNormalization(normalize)
			logCount, templateCount := dm.LoadBaseline(baselineLogs)
			assert.Equal(t, len(baselineLogs), logCount, "Expected baseline logs count to match input")
			assert.Equal(t, 22, templateCount, "Expected logs to be clustered into multiple templates")

			targetLogs := []string{
				"time=2025-07-07T13:59:39.488Z level=INFO source=main.go:725 msg=\"Starting Prometheus Server\" mode=server version=\"(version=3.4.2, branch=HEAD, revision=b392caf256d7ed36980992496c8a6274e5557d36)\"",
				"time=2025-07-07T13:59:39.488Z level=INFO source=main.go:730 msg=\"operational information\" build_context=\"(go=go1.24.4, platform=linux/amd64, user=root@f841ca3a6e68, date=20250626-20:51:48, tags=netgo,builtinassets,stringlabels)\" host_details=\"(Linux 6.6.87.1-microsoft-standard-WSL2 #1 SMP PREEMPT_DYNAMIC Mon Apr 21 17:08:54 UTC 2025 x86_64 prometheus-server-b48bbcb5c-thzp4 localdomain)\" fd_limits=\"(soft=1048576, hard=1048576)\" vm_limits=\"(soft=unlimited, hard=unlimited)\"",
				"time=2025-07-07T13:59:39.489Z level=INFO source=main.go:806 msg=\"Leaving GOMAXPROCS=12: CPU quota undefined\" component=automaxprocs",
				"time=2025-07-07T13:59:39.490Z level=INFO source=web.go:656 msg=\"Start listening for connections\" component=web address=0.0.0.0:9090",
				"time=2025-07-07T13:59:39.491Z level=INFO source=main.go:1266 msg=\"Starting TSDB ...\"",
				"time=2025-07-07T13:59:39.493Z level=INFO source=tls_config.go:347 msg=\"Listening on\" component=web address=[::]:9090",
				"time=2025-07-07T13:59:39.493Z level=INFO source=tls_config.go:350 msg=\"TLS is disabled.\" component=web http2=false address=[::]:9090",
				"time=2025-07-07T13:59:39.496Z level=INFO source=head.go:657 msg=\"Replaying on-disk memory mappable chunks if any\" component=tsdb",
				"time=2025-07-07T13:59:39.496Z level=INFO source=head.go:744 msg=\"On-disk memory mappable chunks replay completed\" component=tsdb duration=833ns",
				"time=2025-07-07T13:59:39.496Z level=INFO source=head.go:752 msg=\"Replaying WAL, this may take a while\" component=tsdb",
				"time=2025-07-07T13:59:39.497Z level=INFO source=head.go:825 msg=\"WAL segment loaded\" component=tsdb segment=0 maxSegment=0 duration=513.055\xc2\xb5s",
				"time=2025-07-07T13:59:39.497Z level=INFO source=head.go:862 msg=\"WAL replay completed\" component=tsdb checkpoint_replay_duration=26.992\xc2\xb5s wal_replay_duration=537.175\xc2\xb5s wbl_replay_duration=130ns chunk_snapshot_load_duration=0s mmap_chunk_replay_duration=833ns total_replay_duration=598.392\xc2\xb5s",
				"time=2025-07-07T13:59:39.499Z level=INFO source=main.go:1287 msg=\"filesystem information\" fs_type=EXT4_SUPER_MAGIC",
				"time=2025-07-07T13:59:39.499Z level=INFO source=main.go:1290 msg=\"TSDB started\"",
				"time=2025-07-07T13:59:39.499Z level=INFO source=main.go:1475 msg=\"Loading configuration file\" filename=/etc/config/prometheus.yml",
				"time=2025-07-07T13:59:39.500Z level=INFO source=kubernetes.go:321 msg=\"Using pod service account via in-cluster config\" component=\"discovery manager scrape\" discovery=kubernetes config=kubernetes-apiservers",
				"time=2025-07-07T13:59:39.501Z level=INFO source=kubernetes.go:321 msg=\"Using pod service account via in-cluster config\" component=\"discovery manager scrape\" discovery=kubernetes config=kubernetes-nodes",
				"time=2025-07-07T13:59:39.501Z level=INFO source=kubernetes.go:321 msg=\"Using pod service account via in-cluster config\" component=\"discovery manager scrape\" discovery=kubernetes config=kubernetes-pods",
				"time=2025-07-07T13:59:39.501Z level=INFO source=kubernetes.go:321 msg=\"Using pod service account via in-cluster config\" component=\"discovery manager scrape\" discovery=kubernetes config=prometheus-pushgateway",
				"time=2025-07-07T13:59:39.502Z level=INFO source=kubernetes.go:321 msg=\"Using pod service account via in-cluster config\" component=\"discovery manager notify\" discovery=kubernetes config=config-0",
				"time=2025-07-07T13:59:39.502Z level=INFO source=main.go:1514 msg=\"updated GOGC\" old=100 new=75",
				"time=2025-07-07T13:59:39.502Z level=INFO source=main.go:1524 msg=\"Completed loading of configuration file\" db_storage=744ns remote_storage=7.272\xc2\xb5s web_handler=160ns query_engine=3.847\xc2\xb5s scrape=224.438\xc2\xb5s scrape_sd=931.323\xc2\xb5s notify=263.143\xc2\xb5s notify_sd=338.273\xc2\xb5s rules=210.977\xc2\xb5s tracing=2.702\xc2\xb5s filename=/etc/config/prometheus.yml totalDuration=3.11457ms",
				"time=2025-07-07T13:59:39.502Z level=INFO source=main.go:1251 msg=\"Server is ready to receive web requests.\"",
				"time=2025-07-07T13:59:39.502Z level=INFO source=manager.go:175 msg=\"Starting rule manager...\" component=\"rule manager\"",
				"time=2025-07-07T13:59:39.505Z level=INFO source=reflector.go:569 msg=\"pkg/mod/k8s.io/client-go@v0.32.3/tools/cache/reflector.go: 251: failed to list *v1.Endpoints: endpoints is forbidden\" component=k8s_client_runtime",
				"time=2025-07-07T13:59:39.506Z level=ERROR source=reflector.go:166 msg=\"Unhandled Error\" component=k8s_client_runtime logger=UnhandledError err=\"pkg/mod/k8s.io/client-go@v0.32.3/tools/cache/reflector.go: 251: Failed to watch *v1.Endpoints\"",
				"time=2025-07-07T13:59:39.506Z level=INFO source=reflector.go:569 msg=\"pkg/mod/k8s.io/client-go@v0.32.3/tools/cache/reflector.go: 251: failed to list *v1.Pod: pods is forbidden\" component=k8s_client_runtime",
				"time=2025-07-07T13:59:39.506Z level=ERROR source=reflector.go:166 msg=\"Unhandled Error\" component=k8s_client_runtime logger=UnhandledError err=\"pkg/mod/k8s.io/client-go@v0.32.3/tools/cache/reflector.go: 251: Failed to watch *v1.Pod\"",
				"time=2025-07-07T13:59:39.506Z level=INFO source=reflector.go:569 msg=\"pkg/mod/k8s.io/client-go@v0.32.3/tools/cache/reflector.go: 251: failed to list *v1.Service: services is forbidden\" component=k8s_client_runtime",
				"time=2025-07-07T13:59:39.506Z level=ERROR source=reflector.go:166 msg=\"Unhandled Error\" component=k8s_client_runtime logger=UnhandledError err=\"pkg/mod/k8s.io/client-go@v0.32.3/tools/cache/reflector.go: 251: Failed to watch *v1.Service\"",
			}

			result, count := dm.AnalyzeTarget(targetLogs)
			assert.Equal(t, 6, len(result), "Expected 6 anomalies to be detected")
			assert.Equal(t, len(targetLogs), count, "Expected target line count to be equal to the number of target logs")
		})
	}
}
