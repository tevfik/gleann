# Graph Report: gleann-real-graph

Generated: 2026-07-23 10:05:36

## Summary

| Metric | Value |
|--------|-------|
| Nodes | 1068 |
| Edges | 2951 |
| Communities | 181 |
| Modularity (Q) | 0.5131 |
| God Nodes | 20 |
| Cross-Community Edges | 20 |

**Modularity interpretation:** Strong community structure (Q > 0.4). Code is well-modularized.

## God Nodes (High-Degree Hubs)

These symbols have an unusually high number of connections, making them central to the codebase.

| Rank | Symbol | Kind | In° | Out° | Total° |
|------|--------|------|-----|------|--------|
| 1 | `httptest.NewRecorder` | function | 258 | 0 | 258 |
| 2 | `httptest.NewRequest` | function | 255 | 0 | 255 |
| 3 | `server.make` | function | 105 | 0 | 105 |
| 4 | `json.NewDecoder` | function | 85 | 0 | 85 |
| 5 | `bytes.NewBufferString` | function | 81 | 0 | 81 |
| 6 | `server.len` | function | 73 | 0 | 73 |
| 7 | `gleann.DefaultConfig` | function | 62 | 0 | 62 |
| 8 | `bytes.NewReader` | function | 48 | 0 | 48 |
| 9 | `json.Marshal` | function | 46 | 0 | 46 |
| 10 | `a2a_cov_test.go` | file | 0 | 42 | 42 |
| 11 | `graph_handler_test.go` | file | 0 | 42 | 42 |
| 12 | `server.writeError` | function | 40 | 1 | 41 |
| 13 | `server.testServer` | function | 39 | 2 | 41 |
| 14 | `coverage_test.go` | file | 0 | 41 | 41 |
| 15 | `openapi_paths.go` | file | 0 | 38 | 38 |
| 16 | `server.writeJSON` | function | 34 | 2 | 36 |
| 17 | `unified_memory_test.go` | file | 0 | 36 | 36 |
| 18 | `unified_cov_test.go` | file | 0 | 36 | 36 |
| 19 | `time.Now` | function | 35 | 0 | 35 |
| 20 | `server.go` | file | 0 | 34 | 34 |

> **Tip:** God nodes are potential coupling hotspots. If a god node changes, many dependents may be affected.

## Communities

Detected via the Louvain algorithm. Each community represents a group of tightly-connected symbols.

### Community 0: graph_handler_test (140 nodes, cohesion=0.021)

- `bytes.NewBufferString`
- `graph_handler_stub_test.go`
- `graph_handler_test.go`
- `handlers_extended_test.go`
- `httptest.NewRecorder`
- `httptest.NewRequest`
- `server.TestHandleAskEmptyNameExt3`
- `server.TestHandleAskEmptyQuestionExt3`
- `server.TestHandleAskIndexNotFoundExt3`
- `server.TestHandleAskInvalidBodyExt3`
- `server.TestHandleAskStreamBadJSON`
- `server.TestHandleAskStreamIndexNotFound`
- `server.TestHandleAskStreamMissingName`
- `server.TestHandleAskStreamMissingQuestion`
- `server.TestHandleAskStreamParamExt3`
- `server.TestHandleAskStreamQueryParam`
- `server.TestHandleAsk_BadJSON`
- `server.TestHandleBlockContext_Empty`
- `server.TestHandleBlockStatsExt`
- `server.TestHandleBuildEmptyExt3`
- ... and 120 more

### Community 1: coverage_test (53 nodes, cohesion=0.044)

- `bytes.NewReader`
- `coverage_test.go`
- `json.Marshal`
- `gleann.DefaultConfig`
- `multi_search_handler_test.go`
- `server.TestBuildProxyMessages_EmptyQuery`
- `server.TestBuildProxyMessages_NoIndexes`
- `server.TestHandleAskWithOptionsExt3`
- `server.TestHandleAsk_EmptyQuestion`
- `server.TestHandleAsk_IndexNotFound`
- `server.TestHandleAsk_MissingName`
- `server.TestHandleAsk_StreamQueryParam`
- `server.TestHandleAsk_WithRole`
- `server.TestHandleBuildBadRequest`
- `server.TestHandleBuildNoTexts`
- `server.TestHandleBuild_EmptyTexts`
- `server.TestHandleBuild_MissingName`
- `server.TestHandleChatCompletions_EmptyMessages`
- `server.TestHandleChatCompletions_EmptyMessagesCov`
- `server.TestHandleChatCompletions_StreamUnknownIndex`
- ... and 33 more

### Community 2: a2a_cov_test (48 nodes, cohesion=0.051)

- `a2a_cov_test.go`
- `os.MkdirAll`
- `os.WriteFile`
- `filepath.Join`
- `server.TestA2AAskHandler_IndexLoadFails`
- `server.TestA2AAskHandler_NoIndexes`
- `server.TestA2AAskHandler_WithMetadataOverride`
- `server.TestA2ACodeHandler_Callees`
- `server.TestA2ACodeHandler_CalleesTurkish`
- `server.TestA2ACodeHandler_CallerError`
- `server.TestA2ACodeHandler_Callers`
- `server.TestA2ACodeHandler_CallersTurkish`
- `server.TestA2ACodeHandler_CallsKeyword`
- `server.TestA2ACodeHandler_DefaultCallees`
- `server.TestA2ACodeHandler_Impact`
- `server.TestA2ACodeHandler_ImpactError`
- `server.TestA2ACodeHandler_IndexNotInPool`
- `server.TestA2ACodeHandler_NilGraphPool`
- `server.TestA2ACodeHandler_NoIndexes`
- `server.TestA2ACodeHandler_ReferencesKeyword`
- ... and 28 more

### Community 3: server_ext2_test (27 nodes, cohesion=0.062)

- `server.TestEventBusWired`
- `server.TestHandleAskBadJSON`
- `server.TestHandleAskEmptyQuestion`
- `server.TestHandleAskIndexNotFound`
- `server.TestHandleAskMissingName`
- `server.TestHandleBuildBadJSON2`
- `server.TestHandleBuildMissingName`
- `server.TestHandleBuildNoTexts2`
- `server.TestHandleDeleteConversationMissingID`
- `server.TestHandleDeleteIndexMissing2`
- `server.TestHandleGetConversationMissingID`
- `server.TestHandleGetConversationNotFound`
- `server.TestHandleGetIndexMissingName2`
- `server.TestHandleHealthResponse`
- `server.TestHandleListConversations2`
- `server.TestHandleListIndexesEmpty2`
- `server.TestHandleMultiSearchBadJSON`
- `server.TestHandleMultiSearchEmptyQuery2`
- `server.TestHandleSearchBadJSON`
- `server.TestHandleSearchEmptyQuery2`
- ... and 7 more

### Community 4: openapi_paths (25 nodes, cohesion=0.012)

- `openapi_paths.go`
- `server.openAPIPathsA2A_part2`
- `server.openAPIPathsBlocks_part2`
- `server.openAPIPathsBlocks_part3`
- `server.openAPIPathsBlocks_part4`
- `server.openAPIPathsBlocks_part5`
- `server.openAPIPathsBlocks_part6`
- `server.openAPIPathsConversations_part2`
- `server.openAPIPathsGraph_part2`
- `server.openAPIPathsGraph_part3`
- `server.openAPIPathsIndexes_part2`
- `server.openAPIPathsIndexes_part3`
- `server.openAPIPathsMemory_part2`
- `server.openAPIPathsMemory_part3`
- `server.openAPIPathsMemory_part4`
- `server.openAPIPathsPacks_part2`
- `server.openAPIPathsPacks_part3`
- `server.openAPIPathsPacks_part4`
- `server.openAPIPathsPacks_part5`
- `server.openAPIPathsProxy_part2`
- ... and 5 more

### Community 5: blocks_handler_test (24 nodes, cohesion=0.113)

- `blocks_handler_test.go`
- `json.NewDecoder`
- `server.TestHandleAddBlock_DefaultsToLongTerm`
- `server.TestHandleAddBlock_InvalidTier`
- `server.TestHandleAddBlock_MediumTier`
- `server.TestHandleAddBlock_RequiresContent`
- `server.TestHandleAddBlock_WithCharLimit`
- `server.TestHandleAddBlock_WithExpiry`
- `server.TestHandleAddBlock_WithScope`
- `server.TestHandleBlockContext_ScopeFilter`
- `server.TestHandleBlockContext_WithBlocks`
- `server.TestHandleBlockContext_XMLFormat`
- `server.TestHandleBlockStats`
- `server.TestHandleClearBlocks_All`
- `server.TestHandleClearBlocks_ByTier`
- `server.TestHandleDeleteBlock_Success`
- `server.TestHandleListBlocks_Empty`
- `server.TestHandleListBlocks_ScopeFilter`
- `server.TestHandleListBlocks_TierFilter`
- `server.TestHandleListBlocks_WithBlocks`
- ... and 4 more

### Community 6: memory_handler_test (23 nodes, cohesion=0.084)

- `memory_handler_test.go`
- `server.TestHandleMemoryDeleteEdge_AfterInject`
- `server.TestHandleMemoryDeleteEdge_InvalidJSON`
- `server.TestHandleMemoryDeleteEdge_NonExistent_StillOK`
- `server.TestHandleMemoryDeleteNode_AfterInject`
- `server.TestHandleMemoryDeleteNode_NonExistent_StillOK`
- `server.TestHandleMemoryInject_EmptyPayload`
- `server.TestHandleMemoryInject_Idempotent`
- `server.TestHandleMemoryInject_InvalidJSON`
- `server.TestHandleMemoryInject_WithNodes`
- `server.TestHandleMemoryInject_WithNodesAndEdges`
- `server.TestHandleMemoryTraverse_DefaultDepth`
- `server.TestHandleMemoryTraverse_EmptyGraph_ReturnsEmptySlices`
- `server.TestHandleMemoryTraverse_InvalidJSON`
- `server.TestHandleMemoryTraverse_MissingStartID`
- `server.TestHandleMemoryTraverse_MultiNodeGraph`
- `server.TestHandleMemoryTraverse_SingleNode`
- `server.TestMemoryEngine_FullLifecycle_part1`
- `server.TestMemoryEngine_FullLifecycle_part2`
- `server.doDeleteEdge`
- ... and 3 more

### Community 7: unified_memory_handler (19 nodes, cohesion=0.016)

- `server.IngestFact`
- `server.IngestRelationship`
- `server.RecallBlock`
- `server.RecallGraph`
- `server.RecallGraphEdge`
- `server.RecallGraphNode`
- `server.RecallHit`
- `server.Server.firstIndexName`
- `server.Server.handleUnifiedIngest_part2`
- `server.Server.handleUnifiedIngest_part3`
- `server.Server.handleUnifiedRecall_part2`
- `server.Server.handleUnifiedRecall_part3`
- `server.Server.mountUnifiedMemory`
- `server.Server.recallGraph`
- `server.UnifiedIngestRequest`
- `server.UnifiedIngestResponse`
- `server.UnifiedRecallRequest`
- `server.UnifiedRecallResponse`
- `unified_memory_handler.go`

### Community 8: openapi_paths (18 nodes, cohesion=0.056)

- `server.TestRefSchema`
- `server.TestRefSchemaCov`
- `server.openAPIPathsA2A_part1`
- `server.openAPIPathsBlocks_part1`
- `server.openAPIPathsConversations_part1`
- `server.openAPIPathsPacks_part1`
- `server.openAPIPathsProxy_part1`
- `server.openAPIPathsTasks_part1`
- `server.openAPIPathsUnifiedMemory_part1`
- `server.openAPIPathsWebhooks_part1`
- `server.openAPISchemasA2A_part1`
- `server.openAPISchemasBlocks_part1`
- `server.openAPISchemasGraph_part1`
- `server.openAPISchemasMemory_part1`
- `server.openAPISchemasProxy_part1`
- `server.openAPISchemasSearch_part1`
- `server.openAPISchemasUnifiedMemory_part1`
- `server.refSchema`

### Community 9: server (18 nodes, cohesion=0.050)

- `openapi.go`
- `server.Server.openAPISpec_part1`
- `server.Server.openAPISpec_part2`
- `server.openAPISchemas`
- `server.openAPISchemasA2A`
- `server.openAPISchemasBlocks`
- `server.openAPISchemasConversations`
- `server.openAPISchemasErrors`
- `server.openAPISchemasGraph`
- `server.openAPISchemasIndex`
- `server.openAPISchemasMemory`
- `server.openAPISchemasPacks`
- `server.openAPISchemasProxy`
- `server.openAPISchemasSearch`
- `server.openAPISchemasTasks`
- `server.openAPISchemasUnifiedMemory`
- `server.openAPISchemasWebhooks`
- `server.swaggerUIHTML`

### Community 10: server (16 nodes, cohesion=0.019)

- `server.Server`
- `server.Server.Bus`
- `server.Server.Start_part2`
- `server.Server.Start_part3`
- `server.Server.handleAsk_part2`
- `server.Server.handleAsk_part3`
- `server.Server.handleBuild_part2`
- `server.Server.handleSearch_part2`
- `server.Server.handleSearch_part3`
- `server.Server.publish`
- `server.askRequest`
- `server.askResponse`
- `server.buildRequest`
- `server.go`
- `server.searchRequest`
- `server.searchResponse`

### Community 11: ratelimit_test (16 nodes, cohesion=0.068)

- `metrics_handler_test.go`
- `ratelimit_test.go`
- `server.TestHandleMetrics`
- `server.TestHandleMetricsEmpty`
- `server.TestMetricsRecordBuildError`
- `server.TestMetricsRecordSearch`
- `server.TestMetrics_RecordAll`
- `server.TestRateLimitMiddleware_AllowsHealthEndpoint`
- `server.TestRateLimitMiddleware_AllowsMetricsEndpoint`
- `server.TestRateLimitMiddleware_Returns429WhenLimited`
- `server.TestRateLimiterAllow`
- `server.TestRateLimiter_AllowsWithinBurst`
- `server.TestRateLimiter_BlocksWhenBucketEmpty`
- `server.TestRateLimiter_IsolatesIPs`
- `server.TestRateLimiter_RefillsOverTime`
- `time.Now`

### Community 12: openapi_schemas (15 nodes, cohesion=0.020)

- `openapi_schemas.go`
- `server.openAPISchemasA2A_part2`
- `server.openAPISchemasA2A_part3`
- `server.openAPISchemasBlocks_part2`
- `server.openAPISchemasGraph_part2`
- `server.openAPISchemasMemory_part2`
- `server.openAPISchemasPacks_part1`
- `server.openAPISchemasPacks_part2`
- `server.openAPISchemasProxy_part2`
- `server.openAPISchemasSearch_part2`
- `server.openAPISchemasSearch_part3`
- `server.openAPISchemasSearch_part4`
- `server.openAPISchemasUnifiedMemory_part2`
- `server.openAPISchemasUnifiedMemory_part3`
- `server.openAPISchemasUnifiedMemory_part4`

### Community 13: memory_handler_stub (15 nodes, cohesion=0.057)

- `json.NewEncoder`
- `memory_handler.go`
- `memory_handler_stub.go`
- `server.DeleteEdgeRequest`
- `server.Server.handleCleanupTasks`
- `server.Server.handleMemoryDeleteEdge`
- `server.Server.handleMemoryDeleteNode`
- `server.Server.handleMemoryInject`
- `server.Server.handleMemoryTraverse`
- `server.Server.handleOpenAPISpec`
- `server.Server.stopMemoryPool`
- `server.SyncerFactory`
- `server.TraverseRequest`
- `server.TraverseResponse`
- `server.memoryPool`

### Community 14: server (15 nodes, cohesion=0.067)

- `server.Server.openAPIPaths`
- `server.openAPIPathsA2A`
- `server.openAPIPathsBlocks`
- `server.openAPIPathsConversations`
- `server.openAPIPathsGraph`
- `server.openAPIPathsHealth`
- `server.openAPIPathsIndexes`
- `server.openAPIPathsMemory`
- `server.openAPIPathsMetrics`
- `server.openAPIPathsPacks`
- `server.openAPIPathsProxy`
- `server.openAPIPathsSearch`
- `server.openAPIPathsTasks`
- `server.openAPIPathsUnifiedMemory`
- `server.openAPIPathsWebhooks`

### Community 15: coverage_test (14 nodes, cohesion=0.071)

- `server.TestClientIP`
- `server.TestClientIPCov_BadRemoteAddr`
- `server.TestClientIPCov_Direct`
- `server.TestClientIPCov_Loopback_XFF`
- `server.TestClientIPCov_Loopback_XRealIP`
- `server.TestClientIP_DirectConnection`
- `server.TestClientIP_DirectConnectionCov`
- `server.TestClientIP_IgnoresXFFFromPublic`
- `server.TestClientIP_TrustsXFFFromPrivate`
- `server.TestClientIP_XForwardedForFromPrivate`
- `server.TestClientIP_XForwardedForFromPublic_Ignored`
- `server.TestClientIP_XRealIPFromLoopback`
- `server.clientIP`
- `server.rateLimiter.middleware`

### Community 16: proxy_handler (14 nodes, cohesion=0.021)

- `proxy_handler.go`
- `server.Server.buildProxyMessages_part2`
- `server.Server.buildProxyMessages_part3`
- `server.Server.handleChatCompletions_part2`
- `server.Server.streamChatCompletions_part2`
- `server.oaiChatRequest`
- `server.oaiChatResponse`
- `server.oaiChoice`
- `server.oaiChunk`
- `server.oaiDelta`
- `server.oaiMessage`
- `server.oaiModel`
- `server.oaiModelList`
- `server.oaiStreamChoice`

### Community 17: proxy_handler_test (14 nodes, cohesion=0.090)

- `context.Background`
- `proxy_handler_test.go`
- `server.Server.injectGraphRelationship`
- `server.TestBuildProxyMessagesNoIndex`
- `server.TestBuildProxyMessagesUnknownIndex`
- `server.TestHandleChatCompletions_InvalidJSON`
- `server.TestHandleListModels_ContentType`
- `server.TestHandleListModels_Empty`
- `server.TestHandleListModels_ModelFields`
- `server.TestListModelsRoute`
- `server.TestOpenAPISpecProxyPaths`
- `server.TestOpenAPISpecProxySchemas`
- `server.TestProxyLLMConfig_Defaults`
- `server.newProxyTestServer`

### Community 18: server_ext3_test (13 nodes, cohesion=0.094)

- `server.TestHandleDeleteConversationEmptyIDExt3`
- `server.TestHandleDeleteConversationNotFoundExt3`
- `server.TestHandleDeleteIndexEmptyNameExt3`
- `server.TestHandleDeleteIndexNotFoundExt3`
- `server.TestHandleGetConversationEmptyIDExt3`
- `server.TestHandleGetConversationNotFoundExt3`
- `server.TestHandleGetIndexEmptyNameExt3`
- `server.TestHandleGetIndexNotFoundExt3`
- `server.TestHandleHealthExt3`
- `server.TestHandleListConversationsExt3`
- `server.TestHandleListIndexesEmptyExt3`
- `server.newTestServerExt3`
- `server_ext3_test.go`

### Community 19: unified_memory_test (12 nodes, cohesion=0.083)

- `server.TestParseDuration`
- `server.TestParseDurationCov`
- `server.TestParseDurationDays`
- `server.TestParseDurationHours`
- `server.TestParseDurationInvalid`
- `server.TestParseDurationMinutes`
- `server.TestParseDurationWeeks`
- `server.TestParseDuration_Days`
- `server.TestParseDuration_Standard`
- `server.TestParseDuration_Weeks`
- `server.parseDuration`
- `strings.TrimSuffix`

### Community 20: http (12 nodes, cohesion=0.136)

- `http.HandlerFunc`
- `http.MaxBytesReader`
- `server.TestCORSMiddleware`
- `server.TestCORSPreflight`
- `server.TestRateLimiterMiddleware`
- `server.TestWithMiddlewareCORSExt3`
- `server.TestWithMiddlewareOptionsExt3`
- `server.TestWithMiddleware_CORSHeaders`
- `server.TestWithMiddleware_OptionsRequest`
- `server.bodyLimitMiddleware`
- `server.rateLimitMiddleware`
- `server.withMiddleware`

### Community 21: graph_handler (11 nodes, cohesion=0.027)

- `graph_handler.go`
- `server.GraphIndexRequest`
- `server.GraphNode`
- `server.GraphQueryRequest`
- `server.GraphQueryResponse`
- `server.GraphStatsResponse`
- `server.ImpactResponse`
- `server.Server.handleGraphQuery_part2`
- `server.Server.handleGraphQuery_part3`
- `server.graphDBHandle`
- `server.graphDBPool`

### Community 22: server (11 nodes, cohesion=0.091)

- `server.Server.handleBlockContext`
- `server.Server.handleBlockStats`
- `server.Server.handleGetIndex`
- `server.Server.handleHealth`
- `server.Server.handleListWebhooks`
- `server.Server.handleSearchBlocks`
- `server.TestWriteJSON`
- `server.TestWriteJSONContentType`
- `server.TestWriteJSONExt3`
- `server.TestWriteJSONExtended`
- `server.writeJSON`

### Community 23: openapi_test (11 nodes, cohesion=0.091)

- `openapi_test.go`
- `server.TestHandleOpenAPISpecCacheable`
- `server.TestHandleOpenAPISpec_part1`
- `server.TestHandleOpenAPISpec_part2`
- `server.TestHandleOpenAPISpec_part3`
- `server.TestHandleSwaggerUI`
- `server.TestOpenAPISpecValidJSON`
- `server.TestOpenAPISpec_BlocksTag`
- `server.TestOpenAPISpec_ErrorSchemas`
- `server.findAllRefs`
- `server.newTestServer`

### Community 24: webhook_validate_test (11 nodes, cohesion=0.083)

- `net.LookupIP`
- `url.Parse`
- `server.TestValidateWebhookURL_BadScheme`
- `server.TestValidateWebhookURL_BlocksPrivate`
- `server.TestValidateWebhookURL_MalformedHost`
- `server.TestValidateWebhookURL_Valid`
- `server.errInvalidWebhookURL`
- `server.isRestrictedIP`
- `server.validateWebhookURL`
- `webhook_validate.go`
- `webhook_validate_test.go`

### Community 25: unified_memory_test (10 nodes, cohesion=0.109)

- `server.TestParseTier`
- `server.TestParseTierCaseInsensitive`
- `server.TestParseTierCov`
- `server.TestParseTierDefault`
- `server.TestParseTierLong`
- `server.TestParseTierMedium`
- `server.TestParseTierShort`
- `server.TestParseTierUnknown`
- `server.parseTier`
- `unified_memory_test.go`

### Community 26: a2a_handler (10 nodes, cohesion=0.200)

- `fmt.Errorf`
- `fmt.Sprintf`
- `multimodal.NewProcessor`
- `server.Server.a2aCodeHandler_part1`
- `server.Server.a2aMemoryHandler_part1`
- `server.Server.a2aMultimodalHandler`
- `server.Server.a2aSearchHandler`
- `server.Server.buildProxyMessages_part1`
- `strings.Join`
- `strings.ToLower`

### Community 27: metrics_handler (9 nodes, cohesion=0.033)

- `metrics_handler.go`
- `server.Server.handleMetrics_part2`
- `server.Server.handleMetrics_part3`
- `server.metrics`
- `server.metrics.RecordAsk`
- `server.metrics.RecordDelete`
- `server.metrics.RecordMultiSearch`
- `server.metrics.RecordWebhook`
- `server.serverMetrics`

### Community 28: unified_memory_test (9 nodes, cohesion=0.111)

- `server.TestParseTimeOrDurationCov`
- `server.TestParseTimeOrDurationGoRelative`
- `server.TestParseTimeOrDurationInvalid`
- `server.TestParseTimeOrDurationRFC3339`
- `server.TestParseTimeOrDuration_Duration`
- `server.TestParseTimeOrDuration_Invalid`
- `server.TestParseTimeOrDuration_RFC3339`
- `server.parseTimeOrDuration`
- `time.Parse`

### Community 29: timeout_test (9 nodes, cohesion=0.118)

- `server.TestPickTimeout`
- `server.TestPickTimeoutCov`
- `server.TestPickTimeout_AskEndpoint`
- `server.TestPickTimeout_BuildEndpoint`
- `server.TestPickTimeout_DefaultEndpoint`
- `server.TestPickTimeout_OpenAPICompletions`
- `server.TestPickTimeout_SearchEndpoint`
- `server.pickTimeout`
- `timeout_test.go`

### Community 30: unified_memory_test (8 nodes, cohesion=0.125)

- `server.Server.handleUnifiedRecall_part1`
- `server.TestLayerSetCaseInsensitive`
- `server.TestLayerSetCov`
- `server.TestLayerSetEmpty`
- `server.TestLayerSetSpecific`
- `server.TestLayerSet_Empty`
- `server.TestLayerSet_Specific`
- `server.layerSet`

### Community 31: ratelimit_test (8 nodes, cohesion=0.125)

- `net.SplitHostPort`
- `server.TestIsPrivate`
- `server.TestIsPrivateCov`
- `server.TestIsPrivateCov2`
- `server.TestIsPrivate_LoopbackIsPrivate`
- `server.TestIsPrivate_PrivateRanges`
- `server.TestIsPrivate_PublicIPNotPrivate`
- `server.isPrivate`

### Community 32: unified_memory_test (8 nodes, cohesion=0.125)

- `server.TestContainsAllTags`
- `server.TestContainsAllTagsCaseInsensitive`
- `server.TestContainsAllTagsCov`
- `server.TestContainsAllTagsCov2`
- `server.TestContainsAllTagsEmpty`
- `server.TestContainsAllTagsMatch`
- `server.TestContainsAllTagsMissing`
- `server.containsAllTags`

### Community 33: scheduler (8 nodes, cohesion=0.159)

- `log.Printf`
- `log.Println`
- `scheduler.go`
- `server.Server.Start_part1`
- `server.runOnce`
- `server.schedulerConfig`
- `server.startMaintenanceScheduler`
- `time.NewTicker`

### Community 34: timeout (7 nodes, cohesion=0.043)

- `server.globalTimeouts`
- `server.timeoutConfig`
- `server.timeoutWriter`
- `server.timeoutWriter.Flush`
- `server.timeoutWriter.Write`
- `server.timeoutWriter.WriteHeader`
- `timeout.go`

### Community 35: helpers_cov_test (7 nodes, cohesion=0.148)

- `helpers_cov_test.go`
- `server.TestBuildRecallContextCov_Empty`
- `server.TestBuildRecallContextCov_WithBlocks`
- `server.TestBuildRecallContextCov_WithGraph`
- `server.TestBuildRecallContextCov_WithVector`
- `server.TestBuildRecallContext_Empty`
- `server.buildRecallContext`

### Community 36: a2a_cov_test (7 nodes, cohesion=0.262)

- `os.Setenv`
- `os.Unsetenv`
- `server.TestMountA2A_AddrWithoutColon`
- `server.TestMountA2A_DisabledViaConfig`
- `server.TestMountA2A_DisabledViaEnv`
- `server.TestMountA2A_EnabledViaEnv`
- `server.newTestMux`

### Community 37: ratelimit_test (7 nodes, cohesion=0.143)

- `net.ParseIP`
- `server.TestSanitizeIP`
- `server.TestSanitizeIPCov`
- `server.TestSanitizeIPCov2`
- `server.TestSanitizeIP_Invalid`
- `server.TestSanitizeIP_Valid`
- `server.sanitizeIP`

### Community 38: server_extended_test (7 nodes, cohesion=0.143)

- `server.Server.handleDeleteBlock`
- `server.Server.handleGraphStats`
- `server.TestWriteError`
- `server.TestWriteErrorBody`
- `server.TestWriteErrorExtended`
- `server.TestWriteError_FormatCov`
- `server.writeError`

### Community 39: timeout_test (7 nodes, cohesion=0.143)

- `server.TestEnvSecondsCov`
- `server.TestEnvSeconds_Empty`
- `server.TestEnvSeconds_InvalidNumber`
- `server.TestEnvSeconds_NegativeNumber`
- `server.TestEnvSeconds_ValidNumber`
- `server.envSeconds`
- `strconv.Atoi`

### Community 40: embedding (7 nodes, cohesion=0.143)

- `background.NewManager`
- `embedding.NewComputer`
- `embedding.Provider`
- `eventbus.New`
- `server.NewServer`
- `server.TestNewServerDefaultVersion`
- `server.TestNewServerDefaultsExt3`

### Community 41: graph_cgo (7 nodes, cohesion=0.114)

- `graph_cgo.go`
- `server.kuzuHandle`
- `server.kuzuHandle.Callees`
- `server.kuzuHandle.Callers`
- `server.kuzuHandle.Close`
- `server.kuzuHandle.SymbolsInFile`
- `server.toGraphNodes`

### Community 42: unified_memory_test (7 nodes, cohesion=0.143)

- `server.Server.handleUnifiedIngest_part1`
- `server.TestTruncateCov`
- `server.TestTruncateExactUMH`
- `server.TestTruncateHelper`
- `server.TestTruncateLongUMH`
- `server.TestTruncateShortUMH`
- `server.truncate`

### Community 43: openapi_paths (7 nodes, cohesion=0.143)

- `server.TestParamName`
- `server.TestParamNameCov`
- `server.openAPIPathsGraph_part1`
- `server.openAPIPathsIndexes_part1`
- `server.openAPIPathsMemory_part1`
- `server.openAPIPathsSearch_part1`
- `server.paramName`

### Community 44: unified_memory_test (6 nodes, cohesion=0.167)

- `server.TestBuildRecallContextAllLayers`
- `server.TestBuildRecallContextEmpty`
- `server.TestBuildRecallContextWithGraph`
- `server.TestBuildRecallContextWithVector`
- `server.TestOpenAPISpecVersionInjection`
- `strings.Contains`

### Community 45: proxy_handler_test (6 nodes, cohesion=0.167)

- `server.TestLastUserContent`
- `server.TestLastUserContentA2A`
- `server.TestLastUserContentA2A_NoUser`
- `server.TestLastUserContentEmpty`
- `server.TestLastUserContentNone`
- `server.lastUserContent`

### Community 46: webhook_handler_test (6 nodes, cohesion=0.233)

- `server.Server.handleDeleteWebhook`
- `server.TestNotifyWebhooksMatchesEvent`
- `server.TestNotifyWebhooksWildcard`
- `server.append`
- `server.kuzuHandle.RawCypher`
- `server.notifyWebhooks`

### Community 47: webhook_handler (6 nodes, cohesion=0.083)

- `server.Server.handleRegisterWebhook`
- `server.Webhook`
- `server.globalWebhooks`
- `server.webhookRegisterRequest`
- `server.webhookStore`
- `webhook_handler.go`

### Community 48: tasks_handler_test (6 nodes, cohesion=0.173)

- `server.TestHandleCleanupTasks`
- `server.TestHandleGetTask`
- `server.TestHandleListTasks`
- `server.TestHandleListTasksFilter`
- `tasks_handler_test.go`
- `time.Sleep`

### Community 49: coverage_test (6 nodes, cohesion=0.200)

- `server.TestGraphDBPool_CloseAll`
- `server.TestHandleMetrics_PrometheusFormat`
- `server.containsSubstring`
- `server.findSubstring`
- `server.kuzuHandle.Impact`
- `server.len`

### Community 50: ratelimit_test (6 nodes, cohesion=0.167)

- `server.TestNewRateLimiter`
- `server.TestNewRateLimiter_EnvVarOverride`
- `server.TestNewRateLimiter_InvalidEnvVars`
- `server.TestRateLimiter_AllowsFirstRequest`
- `server.newRateLimiter`
- `strconv.ParseFloat`

### Community 51: scheduler_test (6 nodes, cohesion=0.173)

- `scheduler_test.go`
- `server.TestNewSchedulerConfig_CustomInterval`
- `server.TestNewSchedulerConfig_Defaults`
- `server.TestNewSchedulerConfig_Disabled`
- `server.TestNewSchedulerConfig_DisabledZero`
- `server.newSchedulerConfig`

### Community 52: timeout_test (6 nodes, cohesion=0.167)

- `context.WithTimeout`
- `server.TestTimeoutMiddlewareSkipsSSE`
- `server.TestTimeoutMiddleware_NormalCompletion`
- `server.TestTimeoutMiddleware_SkipsEventStreamAccept`
- `server.TestTimeoutMiddleware_SkipsSSEStream`
- `server.timeoutMiddleware`

### Community 53: server_extended_test (5 nodes, cohesion=0.165)

- `server.TestNewTimeoutConfig`
- `server.TestNewTimeoutConfig_EnvOverrides`
- `server.newTimeoutConfig`
- `server_extended_test.go`
- `time.Duration`

### Community 54: indexer (5 nodes, cohesion=0.200)

- `indexer.DetectGoModule`
- `indexer.New`
- `server.Server.handleGraphIndex`
- `server.TestGraphStub_RunGraphIndex`
- `server.runGraphIndex`

### Community 55: community (5 nodes, cohesion=0.165)

- `a2a_graph_stub.go`
- `community.DefaultRepoMapConfig`
- `community.GenerateRepoMap`
- `community.NewGraph`
- `server.Server.a2aRepoMapHandler`

### Community 56: gleann (5 nodes, cohesion=0.200)

- `gleann.SearchMultiple`
- `gleann.WithFilterLogic`
- `gleann.WithHybridAlpha`
- `gleann.WithMetadataFilter`
- `server.Server.handleMultiSearch`

### Community 57: a2a_handler (5 nodes, cohesion=0.060)

- `a2a_handler.go`
- `server.Server.a2aAskHandler_part2`
- `server.Server.a2aCodeHandler_part2`
- `server.Server.a2aCodeHandler_part3`
- `server.Server.a2aMemoryHandler_part2`

### Community 58: gleann (5 nodes, cohesion=0.200)

- `gleann.NewReranker`
- `gleann.RerankerProvider`
- `gleann.WithGraphContext`
- `gleann.WithReranker`
- `server.Server.handleSearch_part1`

### Community 59: memory_handler_test (5 nodes, cohesion=0.200)

- `server.TestMemoryPool_CloseAll`
- `server.TestMemoryPool_GetReusesConnection`
- `server.TestMemoryPool_IndependentIndexes`
- `server.TestMemoryStub_CloseAll`
- `server.newMemoryPool`

### Community 60: memory_syncer (5 nodes, cohesion=0.165)

- `kuzu.NewLeannSyncer`
- `gleann.NewBuilder`
- `memory_syncer.go`
- `server.Server.handleBuild_part1`
- `server.Server.initMemorySyncer`

### Community 61: blocks_handler (4 nodes, cohesion=0.075)

- `blocks_handler.go`
- `server.Server.closeBlockMem`
- `server.Server.handleAddBlock_part2`
- `server.blockAddRequest`

### Community 62: memory (4 nodes, cohesion=0.250)

- `memory.DefaultSleepTimeConfig`
- `memory.NewSleepTimeEngine`
- `server.TestStartSleepTimeEngine_DisabledByDefault`
- `server.startSleepTimeEngine`

### Community 63: packs_mount (4 nodes, cohesion=0.192)

- `packs.New`
- `packshttp.New`
- `packs_mount.go`
- `server.Server.mountPacks`

### Community 64: server_test (4 nodes, cohesion=0.250)

- `http.NewServeMux`
- `server.TestHandleDeleteIndexNotFound`
- `server.TestHandleGetIndexNotFound`
- `server.TestMountUnifiedMemory`

### Community 65: context (4 nodes, cohesion=0.250)

- `context.WithCancel`
- `eventbus.DecodePayload`
- `server.TestEventBusSubscribeReceivesPublish`
- `server.cancel`

### Community 66: strings (4 nodes, cohesion=0.250)

- `background.NewAutoIndexer`
- `server.Server.startAutoIndexer`
- `strings.SplitN`
- `strings.TrimSpace`

### Community 67: helpers_cov_test (4 nodes, cohesion=0.250)

- `server.TestParseIndexFromModel`
- `server.TestParseIndexFromModelCov`
- `server.parseIndexFromModel`
- `strings.Split`

### Community 68: a2a_graph_cgo (4 nodes, cohesion=0.217)

- `a2a_graph_cgo.go`
- `community.FromKuzu`
- `server.Server.a2aCommunitiesHandler`
- `server.resolveIndex`

### Community 69: sse_test (4 nodes, cohesion=0.250)

- `json.Unmarshal`
- `server.TestAskRequestStreamDefault`
- `server.TestAskRequestStreamField`
- `server.TestWriteErrorExt3`

### Community 70: graph_cgo (4 nodes, cohesion=0.250)

- `server.int`
- `server.kuzuHandle.EdgeCount`
- `server.kuzuHandle.FileCount`
- `server.kuzuHandle.SymbolCount`

### Community 71: graph_stub (4 nodes, cohesion=0.192)

- `graph_stub.go`
- `server.TestGraphStub_OpenGraphDB`
- `server.graphDBPool.get`
- `server.openGraphDB`

### Community 72: bufio (4 nodes, cohesion=0.250)

- `bufio.NewScanner`
- `server.TestSSEDoneEvent`
- `server.TestSSEDoneSentinel`
- `strings.TrimPrefix`

### Community 73: ratelimit (4 nodes, cohesion=0.075)

- `ratelimit.go`
- `server.bucket`
- `server.globalRateLimiter`
- `server.rateLimiter`

### Community 74: coverage_test (4 nodes, cohesion=0.250)

- `httptest.NewServer`
- `server.TestStreamChatCompletions_Integration`
- `server.TestSyncChatCompletions_Integration`
- `server.TestSyncChatCompletions_LLMError`

### Community 75: unified_memory_handler (4 nodes, cohesion=0.250)

- `server.Server.recallBlocks`
- `server.TestOAIChunkSerialization`
- `server.string`
- `server.titleASCII`

### Community 76: community (4 nodes, cohesion=0.250)

- `community.ComputeRiskScores`
- `community.DefaultRiskConfig`
- `community.TopRisks`
- `server.Server.a2aRiskHandler`

### Community 77: hmac (4 nodes, cohesion=0.250)

- `hmac.New`
- `hex.EncodeToString`
- `http.NewRequest`
- `server.fireWebhookHTTP`

### Community 78: scheduler_test (4 nodes, cohesion=0.250)

- `memory.OpenStore`
- `os.CreateTemp`
- `server.TestRunOnce_DoesNotPanicOnEmptyStore`
- `server.openTempStore`

### Community 79: strings (3 nodes, cohesion=0.333)

- `server.TestSSEEventFormat`
- `strings.HasPrefix`
- `strings.HasSuffix`

### Community 80: unified_memory_graph_stub (3 nodes, cohesion=0.100)

- `server.Server.recallGraphImpl`
- `unified_memory_graph_cgo.go`
- `unified_memory_graph_stub.go`

### Community 81: fmt (3 nodes, cohesion=0.333)

- `fmt.Fprint`
- `server.Server.handleAskStream`
- `server.Server.handleSwaggerUI`

### Community 82: proxy_handler (3 nodes, cohesion=0.333)

- `gleann.ListIndexes`
- `server.Server.handleListIndexes`
- `server.Server.handleListModels`

### Community 83: gleann (3 nodes, cohesion=0.333)

- `gleann.WithTopK`
- `server.Server.recallVector`
- `server.float64`

### Community 84: gleann (3 nodes, cohesion=0.333)

- `gleann.WithMinScore`
- `server.Server.handleChatCompletions_part1`
- `server.float32`

### Community 85: scheduler_test (3 nodes, cohesion=0.333)

- `server.TestStartMaintenanceScheduler_StopsOnClose`
- `server.TestTimeoutMiddleware_Returns504OnTimeout`
- `time.After`

### Community 86: bodylimit (3 nodes, cohesion=0.100)

- `bodylimit.go`
- `server.defaultMaxBodyBytes`
- `server.maxBodyOnce`

### Community 87: eventbus_wiring_test (3 nodes, cohesion=0.217)

- `eventbus_wiring_test.go`
- `server.TestPublishNilSafe`
- `server.recover`

### Community 88: multi_search_handler (3 nodes, cohesion=0.100)

- `multi_search_handler.go`
- `server.multiSearchRequest`
- `server.multiSearchResponse`

### Community 89: blocks_handler (3 nodes, cohesion=0.333)

- `memory.ParseTier`
- `server.Server.handleClearBlocks`
- `server.Server.handleListBlocks`

### Community 90: server (3 nodes, cohesion=0.333)

- `server.delete`
- `server.graphDBPool.close`
- `server.rateLimiter.allow`

### Community 91: metrics_handler (3 nodes, cohesion=0.333)

- `server.int64`
- `server.metrics.RecordBuild`
- `server.metrics.RecordSearch`

### Community 92: os (3 nodes, cohesion=0.333)

- `os.Getenv`
- `server.maxBodyBytes`
- `strconv.ParseInt`

### Community 93: kuzu (3 nodes, cohesion=0.333)

- `kuzu.NewMemoryService`
- `kuzu.Open`
- `server.memoryPool.get`

### Community 94: server (3 nodes, cohesion=0.333)

- `conversations.DefaultStore`
- `server.Server.handleDeleteConversation`
- `server.Server.handleGetConversation`

### Community 95: a2a (3 nodes, cohesion=0.333)

- `a2a.DefaultAgentCard`
- `a2a.NewServer`
- `server.Server.mountA2A`

### Community 96: server (3 nodes, cohesion=0.333)

- `server.Server.Stop`
- `server.TestStartMaintenanceScheduler_NilManagerIsNoop`
- `server.close`

### Community 97: bytes (2 nodes, cohesion=0.500)

- `bytes.Contains`
- `server.TestBuildRecallContext_AllLayers`

### Community 98: fmt (2 nodes, cohesion=0.500)

- `fmt.Fprintf`
- `server.Server.handleMetrics_part1`

### Community 99: memory (2 nodes, cohesion=0.500)

- `memory.DefaultManager`
- `server.Server.blockManager`

### Community 100: unified_memory_handler_test (2 nodes, cohesion=0.500)

- `server.TestBuildRecallContext`
- `server.contains`

### Community 101: background (2 nodes, cohesion=0.500)

- `background.TaskStatus`
- `server.Server.handleListTasks`

### Community 102: gleann (2 nodes, cohesion=0.500)

- `gleann.NewChat`
- `server.Server.syncChatCompletions`

### Community 103: gleann (2 nodes, cohesion=0.500)

- `gleann.LLMProvider`
- `server.Server.a2aAskHandler_part1`

### Community 104: memory (2 nodes, cohesion=0.500)

- `memory.NewManager`
- `server.TestStartSleepTimeEngine_EnabledButNoConvStore`

### Community 105: gleann (2 nodes, cohesion=0.500)

- `gleann.NewSearcher`
- `server.Server.getSearcher`

### Community 106: gleann (2 nodes, cohesion=0.500)

- `gleann.RemoveIndex`
- `server.Server.handleDeleteIndex`

### Community 107: graph_handler (2 nodes, cohesion=0.500)

- `server.Server.handleGraphQuery_part1`
- `time.Since`

### Community 108: roles (2 nodes, cohesion=0.500)

- `roles.DefaultRegistry`
- `server.Server.handleAsk_part1`

### Community 109: unified_memory_test (2 nodes, cohesion=0.500)

- `server.TestBuildRecallContextWithBlocks`
- `time.Date`

### Community 110: server (2 nodes, cohesion=0.500)

- `conversations.ShortID`
- `server.Server.handleListConversations`

### Community 111: tasks_handler (2 nodes, cohesion=0.150)

- `server.Server.mountBackgroundTasks`
- `tasks_handler.go`

### Community 112: proxy_handler (2 nodes, cohesion=0.500)

- `server.Server.streamChatCompletions_part1`
- `server.sendChunk`

### Community 113: http (2 nodes, cohesion=0.500)

- `http.Error`
- `server.Server.handleGetTask`

### Community 114: gleann (2 nodes, cohesion=0.500)

- `gleann.DefaultChatConfig`
- `server.Server.proxyLLMConfig`

### Community 115: fmt (2 nodes, cohesion=0.500)

- `fmt.Sprint`
- `server.loadGraphForA2A`

### Community 116: time (2 nodes, cohesion=0.500)

- `server.Server.handleAddBlock_part1`
- `time.ParseDuration`

### Community 117: gleann (1 nodes, cohesion=1.000)

- `gleann.SearchOption`

### Community 118: community (1 nodes, cohesion=1.000)

- `community.Graph`

### Community 119: graph_handler_test.go (1 nodes, cohesion=1.000)

- `graph_handler_test.go.Server`

### Community 120: gleann (1 nodes, cohesion=1.000)

- `gleann.Config`

### Community 121: gleann (1 nodes, cohesion=1.000)

- `gleann.GraphInjectionPayload`

### Community 122: kuzu (1 nodes, cohesion=1.000)

- `kuzu.MemoryService`

### Community 123: graph_cgo.go (1 nodes, cohesion=1.000)

- `graph_cgo.go.graphDBHandle`

### Community 124: gleann (1 nodes, cohesion=1.000)

- `gleann.SearchResult`

### Community 125: unified_memory_graph_cgo.go (1 nodes, cohesion=1.000)

- `unified_memory_graph_cgo.go.IngestRelationship`

### Community 126: graph_cgo.go (1 nodes, cohesion=1.000)

- `graph_cgo.go.GraphNode`

### Community 127: kuzu (1 nodes, cohesion=1.000)

- `kuzu.DB`

### Community 128: memory (1 nodes, cohesion=1.000)

- `memory.Tier`

### Community 129: http (1 nodes, cohesion=1.000)

- `http.Request`

### Community 130: context (1 nodes, cohesion=1.000)

- `context.Context`

### Community 131: graph_stub.go (1 nodes, cohesion=1.000)

- `graph_stub.go.graphDBHandle`

### Community 132: unified_memory_graph_stub.go (1 nodes, cohesion=1.000)

- `unified_memory_graph_stub.go.IngestRelationship`

### Community 133: graph_handler_test.go (1 nodes, cohesion=1.000)

- `graph_handler_test.go.graphDBHandle`

### Community 134: background (1 nodes, cohesion=1.000)

- `background.AutoIndexer`

### Community 135: embedding (1 nodes, cohesion=1.000)

- `embedding.Computer`

### Community 136: unified_memory_handler_test.go (1 nodes, cohesion=1.000)

- `unified_memory_handler_test.go.Server`

### Community 137: a2a (1 nodes, cohesion=1.000)

- `a2a.SkillContext`

### Community 138: gleann (1 nodes, cohesion=1.000)

- `gleann.MemoryGraphNode`

### Community 139: memory_handler_test.go (1 nodes, cohesion=1.000)

- `memory_handler_test.go.DeleteEdgeRequest`

### Community 140: memory (1 nodes, cohesion=1.000)

- `memory.Manager`

### Community 141: graph_handler_test.go (1 nodes, cohesion=1.000)

- `graph_handler_test.go.ImpactResponse`

### Community 142: graph_handler_test.go (1 nodes, cohesion=1.000)

- `graph_handler_test.go.GraphNode`

### Community 143: unified_cov_test.go (1 nodes, cohesion=1.000)

- `unified_cov_test.go.Server`

### Community 144: time (1 nodes, cohesion=1.000)

- `time.Time`

### Community 145: eventbus (1 nodes, cohesion=1.000)

- `eventbus.Bus`

### Community 146: httptest (1 nodes, cohesion=1.000)

- `httptest.ResponseRecorder`

### Community 147: memory_handler_test.go (1 nodes, cohesion=1.000)

- `memory_handler_test.go.Server`

### Community 148: a2a_cov_test.go (1 nodes, cohesion=1.000)

- `a2a_cov_test.go.Server`

### Community 149: gleann (1 nodes, cohesion=1.000)

- `gleann.Callee`

### Community 150: graph_cgo.go (1 nodes, cohesion=1.000)

- `graph_cgo.go.ImpactResponse`

### Community 151: gleann (1 nodes, cohesion=1.000)

- `gleann.ChatConfig`

### Community 152: unified_memory_graph_cgo.go (1 nodes, cohesion=1.000)

- `unified_memory_graph_cgo.go.RecallGraph`

### Community 153: openapi_test.go (1 nodes, cohesion=1.000)

- `openapi_test.go.Server`

### Community 154: http (1 nodes, cohesion=1.000)

- `http.ResponseWriter`

### Community 155: handlers_extended_test.go (1 nodes, cohesion=1.000)

- `handlers_extended_test.go.Server`

### Community 156: server_ext3_test.go (1 nodes, cohesion=1.000)

- `server_ext3_test.go.Server`

### Community 157: gleann (1 nodes, cohesion=1.000)

- `gleann.MultiSearchResult`

### Community 158: testing (1 nodes, cohesion=1.000)

- `testing.T`

### Community 159: background (1 nodes, cohesion=1.000)

- `background.Manager`

### Community 160: gleann (1 nodes, cohesion=1.000)

- `gleann.Item`

### Community 161: net (1 nodes, cohesion=1.000)

- `net.IP`

### Community 162: gleann (1 nodes, cohesion=1.000)

- `gleann.ChatMessage`

### Community 163: atomic (1 nodes, cohesion=1.000)

- `atomic.Int64`

### Community 164: proxy_handler_test.go (1 nodes, cohesion=1.000)

- `proxy_handler_test.go.Server`

### Community 165: memory_handler_test.go (1 nodes, cohesion=1.000)

- `memory_handler_test.go.TraverseRequest`

### Community 166: http (1 nodes, cohesion=1.000)

- `http.Handler`

### Community 167: sync (1 nodes, cohesion=1.000)

- `sync.RWMutex`

### Community 168: server.go (1 nodes, cohesion=1.000)

- `server.go.graphDBPool`

### Community 169: gleann (1 nodes, cohesion=1.000)

- `gleann.LeannSearcher`

### Community 170: http (1 nodes, cohesion=1.000)

- `http.ServeMux`

### Community 171: http (1 nodes, cohesion=1.000)

- `http.Server`

### Community 172: unified_memory_graph_stub.go (1 nodes, cohesion=1.000)

- `unified_memory_graph_stub.go.RecallGraph`

### Community 173: blocks_handler_test.go (1 nodes, cohesion=1.000)

- `blocks_handler_test.go.Server`

### Community 174: server.go (1 nodes, cohesion=1.000)

- `server.go.memoryPool`

### Community 175: sync (1 nodes, cohesion=1.000)

- `sync.Mutex`

### Community 176: gleann (1 nodes, cohesion=1.000)

- `gleann.MetadataFilter`

### Community 177: gleann (1 nodes, cohesion=1.000)

- `gleann.MemoryGraphEdge`

### Community 178: memory (1 nodes, cohesion=1.000)

- `memory.Store`

### Community 179: blocks_handler_test.go (1 nodes, cohesion=1.000)

- `blocks_handler_test.go.blockAddRequest`

### Community 180: gleann (1 nodes, cohesion=1.000)

- `gleann.LeannChat`

## Cross-Community Edges (Surprising Connections)

These edges connect symbols in different communities, indicating inter-module coupling.
Ranked by composite score: cross-community edges involving different packages score higher.

| From | To | Communities | Score |
|------|----|------------|-------|
| `server.Server.handleBuild_part1` | `server.make` | 60 → 2 | 1.80 |
| `server.Server.handleBuild_part1` | `server.notifyWebhooks` | 60 → 46 | 1.80 |
| `server.Server.handleGraphQuery_part1` | `fmt.Sprintf` | 107 → 26 | 1.80 |
| `server.Server.handleGraphQuery_part1` | `time.Now` | 107 → 11 | 1.80 |
| `server.Server.handleGraphQuery_part1` | `server.writeJSON` | 107 → 22 | 1.80 |
| `server.Server.handleGraphQuery_part1` | `server.len` | 107 → 49 | 1.80 |
| `server.make` | `server.TestRateLimiterAllow` | 2 → 11 | 1.20 |
| `server.Server.handleGraphQuery_part1` | `server.writeError` | 107 → 38 | 1.80 |
| `server.resetWebhooks` | `server.TestNotifyWebhooksMatchesEvent` | 1 → 46 | 1.20 |
| `server.resetWebhooks` | `server.TestNotifyWebhooksWildcard` | 1 → 46 | 1.20 |
| `server.validateWebhookURL` | `os.Getenv` | 24 → 92 | 1.80 |
| `server.validateWebhookURL` | `server.Server.handleRegisterWebhook` | 24 → 47 | 1.80 |
| `server.validateWebhookURL` | `strings.TrimSpace` | 24 → 66 | 1.80 |
| `server.validateWebhookURL` | `fmt.Errorf` | 24 → 26 | 1.50 |
| `server.validateWebhookURL` | `strings.ToLower` | 24 → 26 | 1.50 |
| `server.make` | `server.newTestServer` | 2 → 23 | 1.20 |
| `memory.NewManager` | `server.newA2AServerWithMem` | 104 → 2 | 1.80 |
| `server.Server.handleClearBlocks` | `server.writeJSON` | 89 → 22 | 1.80 |
| `server.TestClientIPCov_Loopback_XRealIP` | `server.make` | 15 → 2 | 1.20 |
| `server.Server.handleClearBlocks` | `server.writeError` | 89 → 38 | 1.80 |

> **Tip:** Many cross-community edges between the same two communities may indicate they should be merged, or there's a missing abstraction layer.

## Suggested Questions

Based on graph structure, these questions may reveal useful insights:

1. What would break if `NewRecorder` (degree 258) were refactored?
2. Is `NewRequest` a genuine hub or should it be split into smaller interfaces?
3. Why do communities 'graph_handler_test' and 'coverage_test' share cross-module edges?
4. What is the relationship between `server.Server.handleBuild_part1` and `server.make` (surprising cross-community edge)?

