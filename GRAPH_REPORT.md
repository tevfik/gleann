# Graph Report: yaver-go

Generated: 2026-08-10 12:24:18

## Summary

| Metric | Value |
|--------|-------|
| Nodes | 7339 |
| Edges | 19408 |
| Communities | 800 |
| Modularity (Q) | 0.5847 |
| God Nodes | 20 |
| Cross-Community Edges | 20 |

**Modularity interpretation:** Strong community structure (Q > 0.4). Code is well-modularized.

## God Nodes (High-Degree Hubs)

These symbols have an unusually high number of connections, making them central to the codebase.

| Rank | Symbol | Kind | In° | Out° | Total° |
|------|--------|------|-----|------|--------|
| 1 | `context.Background` | function | 673 | 0 | 673 |
| 2 | `strings.Contains` | function | 531 | 0 | 531 |
| 3 | `fmt.Sprintf` | function | 401 | 0 | 401 |
| 4 | `filepath.Join` | function | 307 | 0 | 307 |
| 5 | `fmt.Errorf` | function | 276 | 0 | 276 |
| 6 | `httptest.NewServer` | function | 233 | 0 | 233 |
| 7 | `time.Now` | function | 216 | 0 | 216 |
| 8 | `json.NewEncoder` | function | 209 | 0 | 209 |
| 9 | `chattools.New` | function | 196 | 5 | 201 |
| 10 | `http.HandlerFunc` | function | 195 | 0 | 195 |
| 11 | `os.WriteFile` | function | 176 | 0 | 176 |
| 12 | `strings.TrimSpace` | function | 128 | 0 | 128 |
| 13 | `json.NewDecoder` | function | 127 | 0 | 127 |
| 14 | `os.ReadFile` | function | 112 | 0 | 112 |
| 15 | `strings.Join` | function | 103 | 0 | 103 |
| 16 | `llm.New` | function | 90 | 3 | 93 |
| 17 | `strings.ToLower` | function | 92 | 0 | 92 |
| 18 | `json.Unmarshal` | function | 89 | 0 | 89 |
| 19 | `json.Marshal` | function | 87 | 0 | 87 |
| 20 | `slog.Default` | function | 86 | 0 | 86 |

> **Tip:** God nodes are potential coupling hotspots. If a god node changes, many dependents may be affected.

## Communities

Detected via the Louvain algorithm. Each community represents a group of tightly-connected symbols.

### Community 0: devtools (434 nodes, cohesion=0.006)

- `bufio.NewScanner`
- `chat.Agent.systemPrompt`
- `chat.Agent.updatePlanFromResponse_part1`
- `chat.FormatPermissionPrompt`
- `chat.PermissionService.Check`
- `chat.PermissionService.Grant`
- `chat.TestFormatPermissionPrompt`
- `chat.int`
- `chat.parseStepNumber`
- `chattools.ExpandFileRefs_part1`
- `chattools.PermissionConfig.Check`
- `chattools.SlashHandler.CheckPermission`
- `chattools.SlashHandler.Handle_part1`
- `chattools.SlashHandler.RepoPath`
- `chattools.SlashHandler.SetRepoPath`
- `chattools.SlashHandler.generateAgentsMD_part1`
- `chattools.SlashHandler.handleDiff`
- `chattools.SlashHandler.handleEdit_part1`
- `chattools.SlashHandler.handleEdit_part2`
- `chattools.SlashHandler.handleEdit_part3`
- ... and 414 more

### Community 1: client_extended_test (342 nodes, cohesion=0.008)

- `json.NewEncoder`
- `fmt.Fprint`
- `fmt.Fprintf`
- `a2a.Server.handleCard`
- `a2a.Server.handleGetTask`
- `a2a.Server.handleSend_part1`
- `a2a.TestClient_Available`
- `a2a.TestClient_Discover`
- `a2a.TestClient_GetTask`
- `a2a.TestClient_SendMessage`
- `a2a.handler`
- `chattools.SlashHandler.handleSettings_part1`
- `chattools.TestGraphMock_Recall_NoData`
- `chattools.TestGraphMock_Recall_WithData`
- `chattools.TestSlashHandler_Ask`
- `chattools.TestSlashHandler_Search`
- `intent.NewDetector`
- `intent.TestDetectSimple_LLMError_DefaultsToFix`
- `intent.TestDetectSimple_LLMPath`
- `intent.TestDetectSimple_LLMSpecificSEIntents`
- ... and 322 more

### Community 2: dispatch_test (268 nodes, cohesion=0.010)

- `context.Background`
- `chat.TestAgent_ChatWithMeta_Basic`
- `chat.TestAgent_Chat_Basic`
- `chattools.New`
- `chattools.Preview`
- `chattools.TestCheckPermission_SafeCommands`
- `chattools.TestCommands_Content`
- `chattools.TestDirectDiff_NoPending`
- `chattools.TestDirectEdit_AbsPath`
- `chattools.TestDirectEdit_BadMarkers`
- `chattools.TestDirectEdit_EmptyArgs`
- `chattools.TestDirectEdit_FileNotFound`
- `chattools.TestDirectEdit_MultipleMatches`
- `chattools.TestDirectEdit_NoBlock`
- `chattools.TestDirectEdit_PathTraversal`
- `chattools.TestDirectEdit_SearchNotFound`
- `chattools.TestDirectEdit_SuccessfulReplace`
- `chattools.TestDirectGrep_EmptyArgs`
- `chattools.TestDirectGrep_NoMatch`
- `chattools.TestDirectGrep_WithGlob`
- ... and 248 more

### Community 3: manager_extended_test (144 nodes, cohesion=0.018)

- `sql.Open`
- `json.MarshalIndent`
- `chat.Agent.saveHistory`
- `chat.Agent.savePlan`
- `chat.TestAgent_BasicFlow`
- `chat.TestPlanStepMarkers`
- `chat.TestSystemPrompt_AgentsMdEmpty`
- `chat.TestSystemPrompt_AgentsMdTruncation`
- `chattools.ExpandFileRefs`
- `chattools.TestExpandFileRefs_DuplicateRefs`
- `chattools.TestExpandFileRefs_LargeFile`
- `chattools.TestExpandFileRefs_NoRefs`
- `chattools.TestExpandFileRefs_WithFile`
- `chattools.TestUndoStack_Basic`
- `coder.TestBuildAugmentedContext_NoPipeline`
- `coder.TestBuildRunnerDetect`
- `coder.TestContextBuilderBuildWithRepoPathCoverage`
- `coder.TestFileWriterDestructiveBlock`
- `coder.TestFindKeyFiles`
- `coder.TestGetRepoStructure`
- ... and 124 more

### Community 4: gitlab (132 nodes, cohesion=0.016)

- `json.NewDecoder`
- `fmt.Sprintf`
- `chattools.SlashHandler.handleCallees`
- `chattools.SlashHandler.handleCallers`
- `chattools.SlashHandler.handleCommands_part1`
- `chattools.SlashHandler.handleCommunities`
- `chattools.SlashHandler.handleContext`
- `chattools.SlashHandler.handleImpact_part1`
- `chattools.SlashHandler.handleRecall`
- `chattools.SlashHandler.handleRedo`
- `chattools.SlashHandler.handleRemember`
- `chattools.SlashHandler.handleStatus`
- `chattools.SlashHandler.handleUndo`
- `chattools.SlashHandler.handleWebFetch`
- `chattools.SlashHandler.handleWebLinks`
- `coder.BranchManager.SwitchOrCreateBranch_part1`
- `planner.PlanProgress.String`
- `analyzer.extractTypeName`
- `config.fakeInput`
- `dora.Collector.Summary`
- ... and 112 more

### Community 5: gleannmemory (124 nodes, cohesion=0.020)

- `bytes.NewReader`
- `json.Marshal`
- `fmt.Errorf`
- `a2a.Client.Discover`
- `a2a.Client.GetTask`
- `a2a.Client.SendMessage`
- `a2a.string`
- `chattools.mockGraphStore.Callees`
- `chattools.mockGraphStore.Callers`
- `chattools.mockGraphStore.Communities`
- `chattools.mockGraphStore.Impact`
- `chattools.mockGraphStore.Stats`
- `chattools.mockGraphStore.SymbolsInFile`
- `coder.Executor.Execute_part1`
- `intent.Analysis.MarshalJSON`
- `mode.Actor.executeStep`
- `mode.ExecutionPlan.ToJSON`
- `planner.Planner.CreatePlan`
- `planner.TrackedPlan.Pause`
- `planner.TrackedPlan.SkipStep`
- ... and 104 more

### Community 6: cmd (122 nodes, cohesion=0.022)

- `v2.NewProgram`
- `fmt.Fprintln`
- `fmt.Print`
- `fmt.Printf`
- `fmt.Println`
- `yaver-chat.main`
- `chattools.WithMemoryEngine`
- `clarifier.New`
- `handlers.TestSocialSession_UpdatePlan`
- `checkpoint.New`
- `checkpoint.TestCheckpoint_New_Defaults`
- `cmd.Execute`
- `cmd.TestAgentPerformance_part1`
- `cmd.TestDoctor_HostDetection_part1`
- `cmd.TestDoctor_HostDetection_part2`
- `cmd.TestDoctor_LastN`
- `cmd.TestDoctor_OrDefault`
- `cmd.TestInstallCompletions`
- `cmd.TestRemoveCompletions`
- `cmd.TestRunBenchmarkCompare_BadFile`
- ... and 102 more

### Community 7: chat_update_test (66 nodes, cohesion=0.028)

- `tui.TestUpdate_AltEnter_Newline`
- `tui.TestUpdate_AltP_TogglePlan`
- `tui.TestUpdate_ChatResponse_Error`
- `tui.TestUpdate_ChatResponse_Success`
- `tui.TestUpdate_CtrlB_TogglePlanPanel`
- `tui.TestUpdate_CtrlC_CancelStream`
- `tui.TestUpdate_CtrlC_Quit`
- `tui.TestUpdate_CtrlD_StreamingNoText`
- `tui.TestUpdate_CtrlD_WhileWaiting`
- `tui.TestUpdate_CtrlDown`
- `tui.TestUpdate_CtrlF_PageDown`
- `tui.TestUpdate_CtrlL_Clear`
- `tui.TestUpdate_CtrlM_ToggleMouse`
- `tui.TestUpdate_CtrlP_TogglePlan`
- `tui.TestUpdate_CtrlP_WhileStreaming`
- `tui.TestUpdate_CtrlP_WhileWaiting`
- `tui.TestUpdate_CtrlU_PageUp`
- `tui.TestUpdate_CtrlUp`
- `tui.TestUpdate_CtrlY_NoMessages`
- `tui.TestUpdate_CtrlY_WhileStreaming`
- ... and 46 more

### Community 8: coverage_test (60 nodes, cohesion=0.030)

- `json.RawMessage`
- `coder.StepExecutor.executeToolCall`
- `agentregistry.NewSkillCallerAdapter`
- `tools.NewUnifiedRouter`
- `tools.Registry.Unregister`
- `tools.TestExtractA2ATaskContent_FallbackStatus`
- `tools.TestRegisterDefaults_ShellFailure`
- `tools.TestRegistry_ExecuteNilExecute`
- `tools.TestRouter_AvailableToolsMixed`
- `tools.TestRouter_CallA2ASkillNilCaller`
- `tools.TestRouter_DescribeToolsMissingSkill`
- `tools.TestRouter_DescribeToolsMixed`
- `tools.TestRouter_ExecuteA2AErrors`
- `tools.TestRouter_ExecuteA2ANilTask`
- `tools.TestRouter_ExecuteA2APrefixWithoutSkills`
- `tools.TestRouter_ExecuteInvalidJSONParams`
- `tools.TestRouter_ExecuteMCPErrors`
- `tools.TestRouter_ExecuteMCPParseError`
- `tools.TestRouter_ExecuteRegisteredA2ANilSkill`
- `tools.TestUnifiedRouter_A2APrefixed`
- ... and 40 more

### Community 9: mcp_test (59 nodes, cohesion=0.023)

- `mcp.NewManager`
- `mcp.TestAvailableTools`
- `mcp.TestCallToolResultStruct`
- `mcp.TestCallToolServerNotConnected`
- `mcp.TestCallToolUnknown`
- `mcp.TestCallTool_ServerNotConnected`
- `mcp.TestCallTool_Unknown`
- `mcp.TestConnectedServers`
- `mcp.TestConnectedServers_Empty`
- `mcp.TestContentBlockStruct`
- `mcp.TestDiscoverToolsEmpty`
- `mcp.TestDiscoverTools_NoClients`
- `mcp.TestDiscoveredToolStruct`
- `mcp.TestGetServerForTool`
- `mcp.TestGetServerForTool_Unknown`
- `mcp.TestGetToolsForLanguage`
- `mcp.TestGetToolsForLanguage_NoServers`
- `mcp.TestLoadServersNoFile`
- `mcp.TestLoadServersValidConfig`
- `mcp.TestLoadServers_NoFile`
- ... and 39 more

### Community 10: reviewer_di_test (55 nodes, cohesion=0.030)

- `reviewer.NewAgent`
- `reviewer.NewCritic`
- `reviewer.ReviewFilesBatch`
- `reviewer.TestAgentWithChainingAfterDI`
- `reviewer.TestCriticCritiqueCode_LLMError`
- `reviewer.TestCriticCritiqueCode_Success`
- `reviewer.TestCriticCritiquePlan_LLMError`
- `reviewer.TestCriticCritiquePlan_Success`
- `reviewer.TestNewAgentCoverage`
- `reviewer.TestNewAgentWithNilProvider`
- `reviewer.TestNewAgent_Basic`
- `reviewer.TestNewCritic`
- `reviewer.TestNewCriticWithMockProvider`
- `reviewer.TestReviewCodeContextCancellation`
- `reviewer.TestReviewCodeLargeFile_TriggersChunked`
- `reviewer.TestReviewCodeMultiFile_ContextCancelledDuringReview`
- `reviewer.TestReviewCodeMultiFile_PR`
- `reviewer.TestReviewCodeSingleFile_Approved`
- `reviewer.TestReviewCodeSingleFile_ChangesRequested`
- `reviewer.TestReviewCodeSingleFile_LLMError`
- ... and 35 more

### Community 11: checkpoint_test (54 nodes, cohesion=0.036)

- `checkpoint.Checkpoint`
- `checkpoint.Manager`
- `checkpoint.Manager.Clear`
- `checkpoint.Manager.History`
- `checkpoint.Manager.RecordScore`
- `checkpoint.Manager.Rollback`
- `checkpoint.Manager.RollbackLast`
- `checkpoint.Manager.RollbackTo`
- `checkpoint.Manager.SetThreshold`
- `checkpoint.Manager.ShouldAutoRollback`
- `checkpoint.Manager.gitStashPop`
- `checkpoint.Manager.gitStashPush`
- `checkpoint.Manager.rollbackStepUnlocked`
- `checkpoint.NewWithLogger`
- `checkpoint.TestCheckpoint_Clear`
- `checkpoint.TestCheckpoint_Clear_DoesNotAffectGitStashes`
- `checkpoint.TestCheckpoint_CreatedAt`
- `checkpoint.TestCheckpoint_EnsureRepo_IsAGitRepo`
- `checkpoint.TestCheckpoint_EnsureRepo_NotAGitRepo`
- `checkpoint.TestCheckpoint_GitStashPush_Failure`
- ... and 34 more

### Community 12: benchmark_mock_test (50 nodes, cohesion=0.036)

- `assert.Contains`
- `assert.Empty`
- `assert.Equal`
- `assert.Error`
- `assert.False`
- `assert.InDelta`
- `assert.Len`
- `assert.NotContains`
- `assert.NotEmpty`
- `assert.NotNil`
- `assert.True`
- `require.Len`
- `require.NoError`
- `cmd.TestChatWithMock_Failure`
- `cmd.TestChatWithMock_Success`
- `cmd.TestComputeAggregates_MixedResults`
- `cmd.TestComputeAggregates_ZeroLatency`
- `cmd.TestDefaultLLMTasks_NonEmpty`
- `cmd.TestGenerateWithMock`
- `cmd.TestMockMultipleCalls`
- ... and 30 more

### Community 13: decomposer_test (50 nodes, cohesion=0.031)

- `coder.NewTaskDecomposerWithGenerator`
- `coder.SelfReview.autoFix`
- `coder.SelfReview.buildSummary`
- `coder.StepExecutor.callLLMForStep_part1`
- `coder.TaskDecomposer.singleTaskFallback`
- `coder.TestCodingTaskConfig_Fields`
- `coder.TestCodingTaskResult_Fields`
- `coder.TestCreateTasks_EmptySubtasks`
- `coder.TestCreateTasks_MultiWithDeps`
- `coder.TestCreateTasks_NilDecomp`
- `coder.TestCreateTasks_ShortPriorities`
- `coder.TestCreateTasks_Single`
- `coder.TestDecompose_EmptySubtasks`
- `coder.TestDecompose_InvalidJSON`
- `coder.TestDecompose_LLMError`
- `coder.TestDecompose_MarkdownFencedJSON`
- `coder.TestDecompose_NilLLM`
- `coder.TestDecompose_ValidJSON`
- `coder.TestExtractKeywords`
- `coder.TestExtractKeywords_Empty`
- ... and 30 more

### Community 14: chat (48 nodes, cohesion=0.006)

- `tui.ChatFn`
- `tui.ChatFnWithMeta`
- `tui.ChatMessage`
- `tui.ChatModel.Update_part10`
- `tui.ChatModel.Update_part11`
- `tui.ChatModel.Update_part12`
- `tui.ChatModel.Update_part13`
- `tui.ChatModel.Update_part14`
- `tui.ChatModel.Update_part2`
- `tui.ChatModel.Update_part3`
- `tui.ChatModel.Update_part4`
- `tui.ChatModel.Update_part5`
- `tui.ChatModel.Update_part6`
- `tui.ChatModel.Update_part7`
- `tui.ChatModel.Update_part8`
- `tui.ChatModel.Update_part9`
- `tui.ChatModel.View_part2`
- `tui.ChatModel.View_part3`
- `tui.ChatModel.View_part4`
- `tui.ChatModel.View_part5`
- ... and 28 more

### Community 15: webhook_test (46 nodes, cohesion=0.056)

- `hmac.Equal`
- `hmac.New`
- `hex.EncodeToString`
- `apiserver.TestServer_Config`
- `apiserver.TestServer_Health`
- `apiserver.TestServer_Healthz`
- `apiserver.TestServer_Metrics`
- `apiserver.TestServer_Readyz`
- `apiserver.TestServer_Status`
- `apiserver.WithVersion`
- `mcpserver.TestServer_HTTPCallTool_BadRequest`
- `mcpserver.TestServer_HTTPCallTool_Error`
- `mcpserver.TestServer_HTTPListTools`
- `webhook.NewReceiver`
- `webhook.Receiver.validateSignature`
- `webhook.TestEventType_Constants`
- `webhook.TestHTTPHandler_BadBody`
- `webhook.TestHTTPHandler_CommentEvent`
- `webhook.TestHTTPHandler_EmptyBody`
- `webhook.TestHTTPHandler_GetMethod`
- ... and 26 more

### Community 16: handlers (45 nodes, cohesion=0.007)

- `handlers.AssignmentHandler`
- `handlers.AssignmentHandler.GetBase`
- `handlers.AssignmentHandler.Process_part2`
- `handlers.AssignmentHandler.Process_part3`
- `handlers.AssignmentHandler.Process_part4`
- `handlers.AssignmentHandler.handleCodingTask_part2`
- `handlers.AssignmentHandler.handleCodingTask_part3`
- `handlers.AssignmentHandler.handleRelease_part2`
- `handlers.Base`
- `handlers.Base.AddCommentReaction`
- `handlers.Base.AddReaction`
- `handlers.Base.CheckZombieReaction`
- `handlers.Base.MemoryContext`
- `handlers.Base.PostComment`
- `handlers.Base.tryA2ARouting`
- `handlers.CommentHandler`
- `handlers.CommentHandler.GetBase`
- `handlers.CommentHandler.Process_part2`
- `handlers.CommentHandler.Process_part3`
- `handlers.CommentHandler.Process_part4`
- ... and 25 more

### Community 17: client (41 nodes, cohesion=0.007)

- `llm.Client`
- `llm.Client.Chat`
- `llm.Client.ChatStream`
- `llm.Client.ChatWithFormat`
- `llm.Client.ChatWithRetry`
- `llm.Client.ChatWithTools_part2`
- `llm.Client.ChatWithTools_part3`
- `llm.Client.Generate`
- `llm.Client.GenerateStream`
- `llm.Client.GenerateWithTools_part2`
- `llm.Client.GenerateWithTools_part3`
- `llm.Client.InvokeWithRetry`
- `llm.Client.IsOpenAIClient`
- `llm.Client.SetHTTPClient`
- `llm.Client.callOllamaFormatted_part2`
- `llm.Client.callOllamaStream_part2`
- `llm.Client.callOllamaStream_part3`
- `llm.Client.callOllama_part2`
- `llm.Client.callOllama_part3`
- `llm.Client.callOpenAI`
- ... and 21 more

### Community 18: tracker_test (41 nodes, cohesion=0.045)

- `social.Agent.prioritize`
- `social.NewStateTrackerForTest`
- `social.PendingMentionQueue.Len`
- `social.StateTracker.trimErrors`
- `social.TestBottlenecksLimit`
- `social.TestClassifyNotification_Comment`
- `social.TestClassifyNotification_Pull`
- `social.TestCompleteCycleFailure`
- `social.TestCompleteCycleSuccess`
- `social.TestCompletePhase`
- `social.TestCompletePhaseWithError`
- `social.TestDedup_Empty`
- `social.TestDedup_LargeSet`
- `social.TestDedup_NoDuplicates`
- `social.TestDedup_WithDuplicates`
- `social.TestErrorsLimit`
- `social.TestFullOODACycle`
- `social.TestNewStateTrackerForTest`
- `social.TestOODAPhaseString`
- `social.TestPersistence`
- ... and 21 more

### Community 19: gitlab_test (39 nodes, cohesion=0.038)

- `forge.TestGitLab_AddCommentReaction`
- `forge.TestGitLab_AddIssueReaction`
- `forge.TestGitLab_CreateIssue`
- `forge.TestGitLab_CreateIssueComment`
- `forge.TestGitLab_CreatePR`
- `forge.TestGitLab_CreatePRReview_Approve`
- `forge.TestGitLab_CreatePRReview_ApproveStatus`
- `forge.TestGitLab_CreatePRReview_Comment`
- `forge.TestGitLab_CreatePRReview_DefaultComment`
- `forge.TestGitLab_CreatePRReview_RequestChanges`
- `forge.TestGitLab_DeleteCommentReaction`
- `forge.TestGitLab_DeleteIssueReaction`
- `forge.TestGitLab_EditIssueComment`
- `forge.TestGitLab_FindPRByBranch`
- `forge.TestGitLab_GetCommentReactions`
- `forge.TestGitLab_GetIssue`
- `forge.TestGitLab_GetIssueComments`
- `forge.TestGitLab_GetIssueReactions`
- `forge.TestGitLab_GetPR`
- `forge.TestGitLab_GetPRDiff`
- ... and 19 more

### Community 20: chat_coverage_test (37 nodes, cohesion=0.037)

- `tui.BenchmarkRenderSidebar`
- `tui.NewChat`
- `tui.TestCancelStream_NilCancel`
- `tui.TestCancelStream_WithCancel`
- `tui.TestChatModel_Init`
- `tui.TestChatModel_InitialState`
- `tui.TestChromeHeight`
- `tui.TestMultipleOptions`
- `tui.TestNotificationTypes`
- `tui.TestPlanStep_Struct`
- `tui.TestRenderHistoryBlock_Empty`
- `tui.TestRenderHistoryBlock_WithMessages`
- `tui.TestRenderNotification`
- `tui.TestRenderNotification_Expired`
- `tui.TestRenderNotification_NoNotification`
- `tui.TestRenderSidebar_Basic`
- `tui.TestRenderSidebar_Streaming`
- `tui.TestRenderSidebar_Waiting`
- `tui.TestRenderSidebar_WithChangedFiles`
- `tui.TestRenderSidebar_WithContextStatus`
- ... and 17 more

### Community 21: registry_test (34 nodes, cohesion=0.038)

- `tools.NewRegistry`
- `tools.Registry.Count`
- `tools.TestClearExternal`
- `tools.TestCount`
- `tools.TestExecute`
- `tools.TestExecuteNoFunction`
- `tools.TestExecuteNotFound`
- `tools.TestGet`
- `tools.TestGetExternal`
- `tools.TestGetNotFound`
- `tools.TestHas`
- `tools.TestList`
- `tools.TestListExternal`
- `tools.TestListNative`
- `tools.TestNewRegistry`
- `tools.TestRegister`
- `tools.TestRegisterDefaultTools`
- `tools.TestRegisterDefaults_Analyze`
- `tools.TestRegisterDefaults_AnalyzeNoPath`
- `tools.TestRegisterDefaults_FileEditNoPath`
- ... and 14 more

### Community 22: onboarding_test (33 nodes, cohesion=0.037)

- `config.TestDisplaySummary_NoPanic`
- `config.TestInputBool_DefaultNo`
- `config.TestInputBool_Yes`
- `config.TestInputWithDefault_CustomValue`
- `config.TestInputWithDefault_UseDefault`
- `config.TestLoadExistingConfig_Missing`
- `config.TestPrintHeader`
- `config.TestPrintSection`
- `config.TestRun_FullWizard_part2`
- `config.TestRun_FullWizard_part3`
- `config.TestSelectModel_Default`
- `config.TestSelectModel_NameChoice`
- `config.TestSelectModel_NumberChoice`
- `config.TestSetupCompletion_Skip`
- `config.TestSetupCompletion_Uninstall`
- `config.TestSetupForge_GitHub`
- `config.TestSetupForge_Gitea`
- `config.TestSetupForge_None`
- `config.TestSetupGraphStore_Gleann`
- `config.TestSetupGraphStore_InMemory`
- ... and 13 more

### Community 23: handlers_e2e_test (33 nodes, cohesion=0.050)

- `context.WithTimeout`
- `config.Reload`
- `subagent.Executor`
- `e2e.DetectEnv_part1`
- `e2e.MockOllamaServer`
- `e2e.TestAgenticToolLoop_E2E_MockLLM`
- `e2e.TestAgentloopE2E_FullCycle_part1`
- `e2e.TestChatE2E_Headless_part1`
- `e2e.TestCommentFollowUp_part1`
- `e2e.TestCommentFollowUp_part2`
- `e2e.TestCommentFollowUp_part3`
- `e2e.TestErrorRecovery_E2E_MockLLM`
- `e2e.TestIntentDetectionE2E_part1`
- `e2e.TestIntentDetectionE2E_part2`
- `e2e.TestMultiAgentOrchestration_E2E_part1`
- `e2e.TestMultiAgentOrchestration_E2E_part2`
- `e2e.TestSocialAssignment_part1`
- `e2e.TestSocialMention_part1`
- `e2e.TestSocialMention_part2`
- `e2e.TestSocialReview_part1`
- ... and 13 more

### Community 24: timing_extended_test (33 nodes, cohesion=0.048)

- `mcp.Client.Connect`
- `mcp.SSEClient.StartBackground`
- `timing.NewProfileSession`
- `timing.NewProfileTimer`
- `timing.NewTimer`
- `timing.TestProfileSessionBasic`
- `timing.TestProfileSession_AddMetadata`
- `timing.TestProfileSession_AddTimer`
- `timing.TestProfileSession_GetSummary`
- `timing.TestProfileSession_LogSummary`
- `timing.TestProfileTimerAddMetadata`
- `timing.TestProfileTimerBasic`
- `timing.TestProfileTimerEndStageDefault`
- `timing.TestProfileTimerEndStageEmpty`
- `timing.TestProfileTimerGetBreakdown`
- `timing.TestProfileTimerNestedStages`
- `timing.TestProfileTimerStages`
- `timing.TestProfileTimerTotalFormatted`
- `timing.TestProfileTimer_EndStage_Empty`
- `timing.TestProfileTimer_EndStage_NotFound`
- ... and 13 more

### Community 25: release_manual_test (32 nodes, cohesion=0.009)

- `handlers.MockForge`
- `handlers.MockForge.AddCommentReaction`
- `handlers.MockForge.AddIssueReaction`
- `handlers.MockForge.CreateIssue`
- `handlers.MockForge.CreatePR`
- `handlers.MockForge.CreatePRReview`
- `handlers.MockForge.DeleteCommentReaction`
- `handlers.MockForge.DeleteIssueReaction`
- `handlers.MockForge.EditIssueComment`
- `handlers.MockForge.FindPRByBranch`
- `handlers.MockForge.GetCommentReactions`
- `handlers.MockForge.GetIssue`
- `handlers.MockForge.GetIssueComments`
- `handlers.MockForge.GetIssueReactions`
- `handlers.MockForge.GetPR`
- `handlers.MockForge.GetPRDiff`
- `handlers.MockForge.GetRepo`
- `handlers.MockForge.GetUser`
- `handlers.MockForge.ListIssues`
- `handlers.MockForge.ListNotifications`
- ... and 12 more

### Community 26: models (31 nodes, cohesion=0.010)

- `models.AgentResponse`
- `models.AgentResponse.IsSuccess`
- `models.AgentState`
- `models.Comment`
- `models.CycleMetrics`
- `models.ForgeGitea`
- `models.ForgeType`
- `models.Issue`
- `models.LLMMessage`
- `models.LLMResponse`
- `models.LLMRole`
- `models.NewFailureResponse`
- `models.NewSuccessResponse`
- `models.Notification`
- `models.OODAPhase`
- `models.PhaseIdle`
- `models.PriorityLow`
- `models.PullRequest`
- `models.Reaction`
- `models.Repository`
- ... and 11 more

### Community 27: gitea_test (30 nodes, cohesion=0.041)

- `forge.TestGitea_AddCommentReaction`
- `forge.TestGitea_AddIssueReaction`
- `forge.TestGitea_CreateIssue`
- `forge.TestGitea_CreateIssueComment`
- `forge.TestGitea_CreatePR`
- `forge.TestGitea_CreatePRReview`
- `forge.TestGitea_DeleteCommentReaction`
- `forge.TestGitea_DeleteIssueReaction`
- `forge.TestGitea_EditIssueComment`
- `forge.TestGitea_FindPRByBranch`
- `forge.TestGitea_FindPRByBranch_NotFound`
- `forge.TestGitea_GetCommentReactions`
- `forge.TestGitea_GetIssue`
- `forge.TestGitea_GetIssueComments`
- `forge.TestGitea_GetIssueReactions`
- `forge.TestGitea_GetPR`
- `forge.TestGitea_GetPRDiff`
- `forge.TestGitea_GetRepo`
- `forge.TestGitea_ListIssues`
- `forge.TestGitea_ListIssues_DefaultState`
- ... and 10 more

### Community 28: permissions_extended_test (30 nodes, cohesion=0.041)

- `permissions.NewService`
- `permissions.TestCheck_ModeAudit`
- `permissions.TestCheck_ModeCustom_NilPolicy`
- `permissions.TestCheck_ModeCustom_WithPolicy`
- `permissions.TestCheck_ModePlanOnly`
- `permissions.TestCheck_ModeReadOnly_ReadTool`
- `permissions.TestCheck_ModeReadOnly_WriteTool`
- `permissions.TestCheck_ModeYolo`
- `permissions.TestCheck_PathAllowed_PromotesPrompt`
- `permissions.TestCheck_PathDenied`
- `permissions.TestCheck_PathDenied_PlanOnly_NotOverridden`
- `permissions.TestGetMode`
- `permissions.TestGrantTool_ByName`
- `permissions.TestGrantWithPath`
- `permissions.TestGrant_Tool`
- `permissions.TestModeApproveAll`
- `permissions.TestModeAudit`
- `permissions.TestModeCustom`
- `permissions.TestModeDefault_GrantedWrite`
- `permissions.TestModeDefault_ReadOnly`
- ... and 10 more

### Community 29: lifecycle (29 nodes, cohesion=0.034)

- `chat.Agent.Clear`
- `planner.TrackedPlan.Abandon`
- `planner.TrackedPlan.Activate`
- `planner.TrackedPlan.CompleteStep`
- `planner.TrackedPlan.Fail`
- `planner.TrackedPlan.FailStep`
- `planner.TrackedPlan.StartStep`
- `planner.TrackedPlan.completePlan`
- `social.StateTracker.StartCycle`
- `social.StateTracker.StartPhase`
- `git.Client.Commit`
- `gleannmemory.ContextInjector.FetchContextBlock`
- `gleannmemory.MemoryBlocks.StaleBlocks`
- `history.Manager.RecordAnalysis`
- `history.Manager.RecordFileChange`
- `metrics.Collector.RecordDORAMetrics`
- `metrics.Collector.RecordHandler`
- `metrics.Collector.RecordLLMCall`
- `metrics.TestCollectorFormat`
- `metrics.TestCollectorGetDeepCopy`
- ... and 9 more

### Community 30: base_forge_test (28 nodes, cohesion=0.011)

- `handlers.errMockForge`
- `handlers.mockForgeBase`
- `handlers.mockForgeBase.AddCommentReaction`
- `handlers.mockForgeBase.AddIssueReaction`
- `handlers.mockForgeBase.CreateIssue`
- `handlers.mockForgeBase.CreatePR`
- `handlers.mockForgeBase.CreatePRReview`
- `handlers.mockForgeBase.DeleteCommentReaction`
- `handlers.mockForgeBase.DeleteIssueReaction`
- `handlers.mockForgeBase.EditIssueComment`
- `handlers.mockForgeBase.FindPRByBranch`
- `handlers.mockForgeBase.GetCommentReactions`
- `handlers.mockForgeBase.GetIssue`
- `handlers.mockForgeBase.GetIssueComments`
- `handlers.mockForgeBase.GetIssueReactions`
- `handlers.mockForgeBase.GetPR`
- `handlers.mockForgeBase.GetPRDiff`
- `handlers.mockForgeBase.GetRepo`
- `handlers.mockForgeBase.ListIssues`
- `handlers.mockForgeBase.ListNotifications`
- ... and 8 more

### Community 31: lifecycle_test (28 nodes, cohesion=0.044)

- `planner.NewPlanTracker`
- `planner.TestBuildPrompt`
- `planner.TestNewPlanner`
- `planner.TestParsePlan`
- `planner.TestParseStepSimple`
- `planner.TestParseStepWithFiles`
- `planner.TestPlanTracker_Active`
- `planner.TestPlanTracker_Remove`
- `planner.TestPlanTracker_TrackAndGet`
- `planner.TestRegisterTool`
- `planner.TestTrackedPlan_Abandon`
- `planner.TestTrackedPlan_Callbacks`
- `planner.TestTrackedPlan_Fail`
- `planner.TestTrackedPlan_Fail_WithCallback`
- `planner.TestTrackedPlan_FullLifecycle_part1`
- `planner.TestTrackedPlan_FullLifecycle_part2`
- `planner.TestTrackedPlan_InvalidTransitions`
- `planner.TestTrackedPlan_NextPendingStep`
- `planner.TestTrackedPlan_PauseResume`
- `planner.TestTrackedPlan_Progress`
- ... and 8 more

### Community 32: agentloop (28 nodes, cohesion=0.011)

- `agentloop.ConcSafe`
- `agentloop.ConcurrencySafety`
- `agentloop.ContNextTurn`
- `agentloop.ContinueReason`
- `agentloop.LoopConfig`
- `agentloop.LoopDeps`
- `agentloop.LoopParams`
- `agentloop.LoopResult`
- `agentloop.LoopState`
- `agentloop.Message`
- `agentloop.Run_part2`
- `agentloop.Run_part3`
- `agentloop.Run_part4`
- `agentloop.Run_part5`
- `agentloop.Run_part6`
- `agentloop.Run_part7`
- `agentloop.TermCompleted`
- `agentloop.TerminalReason`
- `agentloop.ToolBatch`
- `agentloop.ToolResult`
- ... and 8 more

### Community 33: progress_test (28 nodes, cohesion=0.011)

- `handlers.TestStatusIcons`
- `handlers._`
- `handlers.mockForgeProgress`
- `handlers.mockForgeProgress.AddCommentReaction`
- `handlers.mockForgeProgress.AddIssueReaction`
- `handlers.mockForgeProgress.CreateIssue`
- `handlers.mockForgeProgress.CreateIssueComment`
- `handlers.mockForgeProgress.CreatePR`
- `handlers.mockForgeProgress.CreatePRReview`
- `handlers.mockForgeProgress.DeleteCommentReaction`
- `handlers.mockForgeProgress.DeleteIssueReaction`
- `handlers.mockForgeProgress.EditIssueComment`
- `handlers.mockForgeProgress.FindPRByBranch`
- `handlers.mockForgeProgress.GetCommentReactions`
- `handlers.mockForgeProgress.GetIssue`
- `handlers.mockForgeProgress.GetIssueComments`
- `handlers.mockForgeProgress.GetIssueReactions`
- `handlers.mockForgeProgress.GetPR`
- `handlers.mockForgeProgress.GetPRDiff`
- `handlers.mockForgeProgress.GetRepo`
- ... and 8 more

### Community 34: loop (27 nodes, cohesion=0.011)

- `reasoning.ActPromptMaxChars`
- `reasoning.ContextMaxChars`
- `reasoning.LoopResult`
- `reasoning.MaxActFileLines`
- `reasoning.MaxIterations`
- `reasoning.ReasoningLoop`
- `reasoning.ReasoningLoop.Run_part2`
- `reasoning.ReasoningLoop.SetLLMClient`
- `reasoning.ReasoningLoop.actLoop_part2`
- `reasoning.ReasoningLoop.actLoop_part3`
- `reasoning.ReasoningLoop.actLoop_part4`
- `reasoning.ReasoningLoop.actLoop_part5`
- `reasoning.ReasoningLoop.applyChanges_part2`
- `reasoning.ReasoningLoop.applyChanges_part3`
- `reasoning.ReasoningLoop.applyChanges_part4`
- `reasoning.ReasoningLoop.applyChanges_part5`
- `reasoning.ReasoningLoop.applyChanges_part6`
- `reasoning.ReasoningLoop.applyEditBlocks_part2`
- `reasoning.ReasoningLoop.applyEditBlocks_part3`
- `reasoning.ReasoningLoop.applyLineEdits_part2`
- ... and 7 more

### Community 35: existing_pr_test (27 nodes, cohesion=0.011)

- `handlers.errMockAPI`
- `handlers.mockPRForge`
- `handlers.mockPRForge.AddCommentReaction`
- `handlers.mockPRForge.AddIssueReaction`
- `handlers.mockPRForge.CreateIssue`
- `handlers.mockPRForge.CreateIssueComment`
- `handlers.mockPRForge.CreatePR`
- `handlers.mockPRForge.CreatePRReview`
- `handlers.mockPRForge.DeleteCommentReaction`
- `handlers.mockPRForge.DeleteIssueReaction`
- `handlers.mockPRForge.EditIssueComment`
- `handlers.mockPRForge.FindPRByBranch`
- `handlers.mockPRForge.GetCommentReactions`
- `handlers.mockPRForge.GetIssue`
- `handlers.mockPRForge.GetIssueComments`
- `handlers.mockPRForge.GetIssueReactions`
- `handlers.mockPRForge.GetPR`
- `handlers.mockPRForge.GetPRDiff`
- `handlers.mockPRForge.GetRepo`
- `handlers.mockPRForge.ListIssues`
- ... and 7 more

### Community 36: config (27 nodes, cohesion=0.011)

- `config.AgentEndpoint`
- `config.AgentsConfig`
- `config.Config`
- `config.Config.ModelFor`
- `config.Config.Validate_part2`
- `config.ForgeConfig`
- `config.GitConfig`
- `config.GleannConfig`
- `config.GraphConfig`
- `config.LogConfig`
- `config.MCPConfig`
- `config.MCPServerEntry`
- `config.MemoryConfig`
- `config.Neo4jConfig`
- `config.OllamaConfig`
- `config.OpenAIConfig`
- `config.ProjectConfig`
- `config.QdrantConfig`
- `config.RepoRef`
- `config.SocialConfig`
- ... and 7 more

### Community 37: agent_forge_test (27 nodes, cohesion=0.011)

- `social.mockForgeAgent`
- `social.mockForgeAgent.AddCommentReaction`
- `social.mockForgeAgent.AddIssueReaction`
- `social.mockForgeAgent.CreateIssue`
- `social.mockForgeAgent.CreateIssueComment`
- `social.mockForgeAgent.CreatePR`
- `social.mockForgeAgent.CreatePRReview`
- `social.mockForgeAgent.DeleteCommentReaction`
- `social.mockForgeAgent.DeleteIssueReaction`
- `social.mockForgeAgent.EditIssueComment`
- `social.mockForgeAgent.FindPRByBranch`
- `social.mockForgeAgent.GetCommentReactions`
- `social.mockForgeAgent.GetIssue`
- `social.mockForgeAgent.GetIssueComments`
- `social.mockForgeAgent.GetIssueReactions`
- `social.mockForgeAgent.GetPR`
- `social.mockForgeAgent.GetPRDiff`
- `social.mockForgeAgent.GetRepo`
- `social.mockForgeAgent.ListIssues`
- `social.mockForgeAgent.ListNotifications`
- ... and 7 more

### Community 38: github_test (27 nodes, cohesion=0.047)

- `forge.TestGitHub_AddCommentReaction`
- `forge.TestGitHub_AddIssueReaction`
- `forge.TestGitHub_CreateIssue`
- `forge.TestGitHub_CreateIssueComment`
- `forge.TestGitHub_CreatePR`
- `forge.TestGitHub_CreatePRReview`
- `forge.TestGitHub_DeleteCommentReaction`
- `forge.TestGitHub_DeleteIssueReaction`
- `forge.TestGitHub_EditIssueComment`
- `forge.TestGitHub_FindPRByBranch`
- `forge.TestGitHub_FindPRByBranch_None`
- `forge.TestGitHub_GetCommentReactions`
- `forge.TestGitHub_GetIssue`
- `forge.TestGitHub_GetIssueComments`
- `forge.TestGitHub_GetIssueReactions`
- `forge.TestGitHub_GetPR`
- `forge.TestGitHub_GetPRDiff`
- `forge.TestGitHub_GetRepo`
- `forge.TestGitHub_ListIssues`
- `forge.TestGitHub_ListNotifications`
- ... and 7 more

### Community 39: cleanup_extended_test (26 nodes, cohesion=0.078)

- `session.CleanupExpiredSessions`
- `session.CleanupOldRepos`
- `session.CleanupResult`
- `session.RunCleanup`
- `session.TestCleanupExpiredSessions_DefaultExpiration`
- `session.TestCleanupExpiredSessions_DryRunMode`
- `session.TestCleanupExpiredSessions_EmptySessionDir`
- `session.TestCleanupExpiredSessions_NonexistentDir`
- `session.TestCleanupExpiredSessions_RemovesExpired`
- `session.TestCleanupExpiredSessions_SkipsFiles`
- `session.TestCleanupOldRepos_DefaultCleanupDays`
- `session.TestCleanupOldRepos_DryRun`
- `session.TestCleanupOldRepos_EmptyDir`
- `session.TestCleanupOldRepos_ExcludesRepos`
- `session.TestCleanupOldRepos_NonexistentDir`
- `session.TestCleanupOldRepos_RemovesOldGitRepos`
- `session.TestCleanupOldRepos_SkipsNonGitDirs`
- `session.TestRunCleanup_EmptyPaths`
- `session.TestRunCleanup_WithTempDirs`
- `session.dirSize`
- ... and 6 more

### Community 40: plan_test (25 nodes, cohesion=0.044)

- `chat.Agent.InjectMemoryContext`
- `chat.Agent.InjectToolDescriptions`
- `chat.Agent.LastHumanMessage`
- `chat.Agent.buildContextualPrompt_part1`
- `chat.Agent.loadHistory`
- `chat.Agent.planContext`
- `chat.LoopAdapter.makeTokenCounter`
- `chat.TestAgent_BuildMessages`
- `chat.TestAgent_OtherUtils`
- `chat.TestAgent_PlanFlow`
- `chat.TestAgent_SaveLoadHistory`
- `chat.TestParseToolCallsFromContent_NoTools`
- `chat.TestParseToolCallsFromContent_WithTools`
- `chat.TestPlanAutoDetectFromResponse`
- `chat.TestPlanContext`
- `chat.TestPlanNotOverwrittenByNonPlan`
- `chat.TestPlanNotReplacedDuringExecution`
- `chat.TestPlanPersistence`
- `chat.TestPlanReplacementAfterAllDone`
- `chat.TestSetNextImages`
- ... and 5 more

### Community 41: gitea_errors_test (25 nodes, cohesion=0.050)

- `forge.TestGitea_AddCommentReaction_HTTP500`
- `forge.TestGitea_AddIssueReaction_HTTP500`
- `forge.TestGitea_CreateIssueComment_HTTP500`
- `forge.TestGitea_CreateIssue_HTTP500`
- `forge.TestGitea_CreatePRReview_HTTP500`
- `forge.TestGitea_CreatePR_HTTP500`
- `forge.TestGitea_DeleteCommentReaction_HTTP500`
- `forge.TestGitea_DeleteIssueReaction_HTTP500`
- `forge.TestGitea_EditIssueComment_HTTP500`
- `forge.TestGitea_FindPRByBranch_HTTP500`
- `forge.TestGitea_GetCommentReactions_HTTP500`
- `forge.TestGitea_GetIssueComments_HTTP500`
- `forge.TestGitea_GetIssueReactions_HTTP500`
- `forge.TestGitea_GetIssue_HTTP500`
- `forge.TestGitea_GetPRDiff_HTTP500`
- `forge.TestGitea_GetPR_HTTP500`
- `forge.TestGitea_GetRepo_HTTP500`
- `forge.TestGitea_ListIssues_HTTP500`
- `forge.TestGitea_ListNotifications_HTTP500`
- `forge.TestGitea_ListPRs_HTTP500`
- ... and 5 more

### Community 42: gitlab_errors_test (25 nodes, cohesion=0.050)

- `forge.TestGitLab_AddCommentReaction_HTTP500`
- `forge.TestGitLab_AddIssueReaction_HTTP500`
- `forge.TestGitLab_CreateIssueComment_HTTP500`
- `forge.TestGitLab_CreateIssue_HTTP500`
- `forge.TestGitLab_CreatePRReview_HTTP500`
- `forge.TestGitLab_CreatePR_HTTP500`
- `forge.TestGitLab_DeleteCommentReaction_HTTP500`
- `forge.TestGitLab_DeleteIssueReaction_HTTP500`
- `forge.TestGitLab_EditIssueComment_HTTP500`
- `forge.TestGitLab_FindPRByBranch_HTTP500`
- `forge.TestGitLab_GetCommentReactions_HTTP500`
- `forge.TestGitLab_GetIssueComments_HTTP500`
- `forge.TestGitLab_GetIssueReactions_HTTP500`
- `forge.TestGitLab_GetIssue_HTTP500`
- `forge.TestGitLab_GetPRDiff_500Body`
- `forge.TestGitLab_GetPR_HTTP500`
- `forge.TestGitLab_GetRepo_HTTP500`
- `forge.TestGitLab_ListIssues_HTTP500`
- `forge.TestGitLab_ListNotifications_HTTP500`
- `forge.TestGitLab_ListPRs_HTTP500`
- ... and 5 more

### Community 43: client (25 nodes, cohesion=0.012)

- `mcp.CallToolResult`
- `mcp.Client`
- `mcp.Client.Disconnect`
- `mcp.Client.Info`
- `mcp.Client.IsConnected`
- `mcp.Client.ListTools`
- `mcp.Client.Tools`
- `mcp.Client.cleanup`
- `mcp.Client.connectHTTP`
- `mcp.ClientConfig`
- `mcp.ConnectionError`
- `mcp.ConnectionError.Error`
- `mcp.ContentBlock`
- `mcp.Resource`
- `mcp.ServerInfo`
- `mcp.TimeoutError`
- `mcp.TimeoutError.Error`
- `mcp.Tool`
- `mcp.ToolError`
- `mcp.TransportStdio`
- ... and 5 more

### Community 44: a2a (25 nodes, cohesion=0.012)

- `a2a.AgentCapabilities`
- `a2a.AgentCard`
- `a2a.AgentInterface`
- `a2a.AgentSkill`
- `a2a.Artifact`
- `a2a.Client`
- `a2a.Client.Available`
- `a2a.DefaultYaverCard_part1`
- `a2a.DefaultYaverCard_part2`
- `a2a.ErrorResponse`
- `a2a.Message`
- `a2a.Part`
- `a2a.SendMessageRequest`
- `a2a.SendMessageResponse`
- `a2a.Server`
- `a2a.Server.Mount`
- `a2a.Server.RegisterSkill`
- `a2a.Server.handleSend_part2`
- `a2a.Server.handleSend_part3`
- `a2a.SkillHandler`
- ... and 5 more

### Community 45: github_errors_test (25 nodes, cohesion=0.050)

- `forge.TestGitHub_AddCommentReaction_HTTP500`
- `forge.TestGitHub_AddIssueReaction_HTTP500`
- `forge.TestGitHub_CreateIssueComment_HTTP500`
- `forge.TestGitHub_CreateIssue_HTTP500`
- `forge.TestGitHub_CreatePRReview_HTTP500`
- `forge.TestGitHub_CreatePR_HTTP500`
- `forge.TestGitHub_DeleteCommentReaction_HTTP500`
- `forge.TestGitHub_DeleteIssueReaction_HTTP500`
- `forge.TestGitHub_EditIssueComment_HTTP500`
- `forge.TestGitHub_FindPRByBranch_HTTP500`
- `forge.TestGitHub_GetCommentReactions_HTTP500`
- `forge.TestGitHub_GetIssueComments_HTTP500`
- `forge.TestGitHub_GetIssueReactions_HTTP500`
- `forge.TestGitHub_GetIssue_HTTP500`
- `forge.TestGitHub_GetPRDiff_500Body`
- `forge.TestGitHub_GetPR_HTTP500`
- `forge.TestGitHub_GetRepo_HTTP500`
- `forge.TestGitHub_ListIssues_HTTP500`
- `forge.TestGitHub_ListNotifications_HTTP500`
- `forge.TestGitHub_ListPRs_HTTP500`
- ... and 5 more

### Community 46: coverage_test (24 nodes, cohesion=0.056)

- `chat.Agent.ChatWithMeta`
- `chat.Agent.History`
- `chat.Agent.SetNextImages`
- `chat.TestAgent_BuildContextualPrompt`
- `chat.TestBuildContextualPrompt_Compact`
- `chat.TestBuildContextualPrompt_Short`
- `chat.TestBuildContextualPrompt_SoftTruncate`
- `chat.TestBuildMessages_AgenticContinuation`
- `chat.TestBuildMessages_NormalWithImages`
- `chat.TestExtractPlanFromContent_Max15Steps`
- `chat.TestInjectMemoryContext_EmptyHistory`
- `chat.TestInjectMemoryContext_NonEmpty`
- `chat.TestInjectToolDescriptions_EmptyHistory`
- `chat.TestInjectToolDescriptions_NonEmpty`
- `chat.TestLastHumanMessage`
- `chat.TestPlanContext_AllStatuses`
- `chat.TestSavePlan_WithSteps`
- `chat.TestSetRepoPath_RebuildsPrompt`
- `chat.TestSystemPrompt_AgentsMdMissing`
- `chat.TestUpdatePlanFromResponse_AllowReplacementWhenAllDone`
- ... and 4 more

### Community 47: zombie_coverage_test (24 nodes, cohesion=0.057)

- `handlers.TestCheckZombieCommentReaction_AlreadyProcessed`
- `handlers.TestCheckZombieCommentReaction_Error`
- `handlers.TestCheckZombieCommentReaction_EyesFresh`
- `handlers.TestCheckZombieCommentReaction_EyesStaleAtMaxRetries`
- `handlers.TestCheckZombieCommentReaction_EyesStaleBelowMaxRetries`
- `handlers.TestCheckZombieCommentReaction_NoReactions`
- `handlers.TestCheckZombieCommentReaction_UserCancelledConfused`
- `handlers.TestCheckZombieCommentReaction_UserCancelledMinusOne`
- `handlers.TestCheckZombieCommentReaction_UserCancelledThumbsDown`
- `handlers.TestCheckZombieReactionEnhanced_AlreadyProcessed`
- `handlers.TestCheckZombieReactionEnhanced_Errors`
- `handlers.TestCheckZombieReactionEnhanced_EyesFresh`
- `handlers.TestCheckZombieReactionEnhanced_EyesStaleAtMaxRetries`
- `handlers.TestCheckZombieReactionEnhanced_EyesStaleBelowMaxRetries`
- `handlers.TestCheckZombieReactionEnhanced_NoReactions`
- `handlers.TestCheckZombieReactionEnhanced_NonAgentReaction`
- `handlers.TestCheckZombieReactionEnhanced_Thumbsup`
- `handlers.TestCheckZombieReactionEnhanced_UserCancelled`
- `handlers.TestCheckZombieReactionEnhanced_UserCancelledConfused`
- `handlers.TestRemoveReaction_NoPanic`
- ... and 4 more

### Community 48: history_test (23 nodes, cohesion=0.067)

- `history.TestAnalysisEntry_Fields`
- `history.TestCleanupOldAnalyses`
- `history.TestCleanupOldAnalyses_Empty`
- `history.TestClearAll`
- `history.TestDeleteProject`
- `history.TestGetAllProjects`
- `history.TestGetFileVolatility_NotFound`
- `history.TestGetHistory`
- `history.TestGetHistory_Empty`
- `history.TestGetLastAnalysis_NotFound`
- `history.TestReanalyzeReason_Constants`
- `history.TestRecordAnalysis_And_GetLastAnalysis`
- `history.TestRecordAnalysis_MultipleCommits`
- `history.TestRecordAnalysis_Upsert`
- `history.TestRecordFileChange_And_GetVolatility`
- `history.TestShouldReanalyze_FirstTime`
- `history.TestShouldReanalyze_NewCommit`
- `history.TestShouldReanalyze_NoChanges`
- `history.len`
- `history.newTestManager`
- ... and 3 more

### Community 49: stdio_test (23 nodes, cohesion=0.059)

- `coder.ContextBuilder.log`
- `coder.NewBranchManagerWithToken`
- `mode.NewPlanner`
- `handlers.TestGleannPipeline_Configured`
- `handlers.TestGleannPipeline_DefaultIndexName`
- `handlers.TestGleannPipeline_FallbackToGraphURL`
- `handlers.TestGleannPipeline_NotConfigured`
- `mcpserver.NewServer`
- `mcpserver.TestServer_CallTool`
- `mcpserver.TestServer_CallTool_NotFound`
- `mcpserver.TestStdio_Initialize`
- `mcpserver.TestStdio_NotificationsInitialized`
- `mcpserver.TestStdio_ToolsCall`
- `mcpserver.TestStdio_ToolsCall_InvalidParams`
- `mcpserver.TestStdio_ToolsCall_ToolError`
- `mcpserver.TestStdio_ToolsList`
- `mcpserver.TestStdio_UnknownMethod`
- `mcpserver.TestStdio_WriteError`
- `mcpserver.TestStdio_WriteResult`
- `mcpserver.float64`
- ... and 3 more

### Community 50: base_forge_test (23 nodes, cohesion=0.041)

- `handlers.NewBase`
- `handlers.TestAddCommentReaction_Error`
- `handlers.TestAddCommentReaction_Success`
- `handlers.TestAddReaction_Error`
- `handlers.TestAddReaction_Success`
- `handlers.TestBaseHandler_Constructor_Nil`
- `handlers.TestBaseHandler_MemoryContext_NoPanic`
- `handlers.TestCheckZombieReaction_ForgeError`
- `handlers.TestCheckZombieReaction_NotProcessed`
- `handlers.TestCheckZombieReaction_Processed_Confused`
- `handlers.TestCheckZombieReaction_Processed_Plus1`
- `handlers.TestCheckZombieReaction_Processed_Thumbsup`
- `handlers.TestCloneOrOpenRepo_DirNoGit`
- `handlers.TestCloneOrOpenRepo_NoURL`
- `handlers.TestMockForge_ImplementsProvider`
- `handlers.TestNewBase`
- `handlers.TestNewBaseWithMock`
- `handlers.TestNewBaseWithNil`
- `handlers.TestPostComment_Error`
- `handlers.TestPostComment_Success`
- ... and 3 more

### Community 51: os (23 nodes, cohesion=0.075)

- `chat.TestSaveLoadHistory`
- `chat.TestSavePlan_EmptyPlan`
- `chattools.TestUndoStack_NewFile`
- `reasoning.TestReasoningLoop_ApplyChanges_SkipsPlaceholderPaths`
- `handlers.TestSocialSession_BasicFlow`
- `agentregistry.TestFileDiscovery_RegisterAndUnregister`
- `agentregistry.UnregisterLocal`
- `checkpoint.Manager.repoSafe`
- `cmd.TestSafeRmFile_NotExists`
- `cmd.TestSafeRmFile_WithForce`
- `cmd.TestVSCode_PrintModeIsReadOnly`
- `cmd.runVSCode`
- `cmd.safeRmFile`
- `config.TestHomeDir`
- `config.TestHomeDir_CreatesDirectory`
- `history.TestNew_DefaultPath`
- `session.Manager.ensureFile`
- `tui.NeedsOnboarding`
- `tui.TestNeedsOnboarding`
- `integration.TestFileDiscovery_Integration`
- ... and 3 more

### Community 52: prompt_test (23 nodes, cohesion=0.057)

- `prompt.NewRegistry`
- `prompt.TestCoderSystemContent`
- `prompt.TestCriticSystemContent`
- `prompt.TestCriticalTemplatesPresent`
- `prompt.TestEmbeddedTemplatesLoaded`
- `prompt.TestGet`
- `prompt.TestGetNotFound`
- `prompt.TestList`
- `prompt.TestMustGet`
- `prompt.TestNewRegistry`
- `prompt.TestOverrideTemplate`
- `prompt.TestRegister`
- `prompt.TestRegistryRender`
- `prompt.TestRegistryRenderNotFound`
- `prompt.TestRenderCoderUser`
- `prompt.TestRenderCommitMessage`
- `prompt.TestRenderErrorFixer`
- `prompt.TestRenderIntentDetection`
- `prompt.TestRenderPreservesJSONBraces`
- `prompt.TestReviewerSystemContent`
- ... and 3 more

### Community 53: pipeline_test (22 nodes, cohesion=0.075)

- `parallelreview.New`
- `parallelreview.NewWithDefaults`
- `parallelreview.TestMerge_CollectsAllIssues`
- `parallelreview.TestMerge_SomeRejected`
- `parallelreview.TestNewWithDefaults`
- `parallelreview.TestNew_Defaults`
- `parallelreview.TestPipeline_MixedReviews`
- `parallelreview.TestPipeline_Progress`
- `parallelreview.TestPipeline_Review`
- `parallelreview.TestPipeline_Review_Concurrency`
- `parallelreview.TestPipeline_Review_ContextCancel`
- `parallelreview.TestPipeline_Review_FailedTasks`
- `parallelreview.TestPipeline_Review_NilReviewer`
- `parallelreview.TestPipeline_Review_NoTasks`
- `parallelreview.TestPipeline_Review_Timeout`
- `parallelreview.close`
- `parallelreview.defaultLogger`
- `parallelreview.len`
- `parallelreview.make`
- `parallelreview.mockMixedReviewer`
- ... and 2 more

### Community 54: factory_test (21 nodes, cohesion=0.041)

- `featurefactory.New`
- `featurefactory.TestImplementDecomposeFail`
- `featurefactory.TestImplementExecuteError`
- `featurefactory.TestImplementExecuteFail`
- `featurefactory.TestImplementNoQuality`
- `featurefactory.TestImplementNoSubtasks`
- `featurefactory.TestImplementPlanFail`
- `featurefactory.TestImplementSkipsRootTask`
- `featurefactory.TestImplementSuccess`
- `featurefactory.TestNew`
- `featurefactory.TestPartialFailure`
- `featurefactory.TestQualityReportFields`
- `featurefactory.TestRequirementPreserved`
- `featurefactory.TestTaskFields`
- `featurefactory.failExecute`
- `featurefactory.okDecompose`
- `featurefactory.okExecute`
- `featurefactory.okPlan`
- `featurefactory.okQuality`
- `factory_test.go`
- ... and 1 more

### Community 55: client (20 nodes, cohesion=0.050)

- `json.Unmarshal`
- `chat.Agent.loadPlan`
- `mode.TestExecutionPlan_ToJSON`
- `social.StateTracker.load`
- `checkpoint.TestCheckpoint_CheckpointJSONMarshalRoundTrip`
- `cmd.TestExtractJSONFromProse_part1`
- `graphstore.TestSymbolJSON`
- `llm.Client.GenerateJSON`
- `mcp.Client.CallTool`
- `mcp.Client.ListResources`
- `mcp.Client.handleSSEEvent`
- `mcp.Client.initialize`
- `mcp.Client.loadTools`
- `mcp.TestJSONRPCResponseUnmarshal`
- `mcp.TestJSONRPCResponseWithError`
- `mcpserver.Server.handleStdioMessage`
- `session.Manager.readStore`
- `webhook.Receiver.parseComment`
- `webhook.Receiver.parseEvent_part1`
- `webhook.Receiver.parseIDs`

### Community 56: coverage_test (20 nodes, cohesion=0.075)

- `context.WithCancel`
- `gleannmemory.BackgroundExtractor.Start`
- `mcp.Client.EnableSSE`
- `mcp.NewSSEClient`
- `mcp.SSEClient.Start`
- `mcp.TestHandleSSEEvent_EndpointEvent`
- `mcp.TestHandleSSEEvent_ErrorEvent`
- `mcp.TestHandleSSEEvent_ToolListChanged_part1`
- `mcp.TestHandleSSEEvent_ToolListChanged_part2`
- `mcp.TestSSEClient_DoubleStart`
- `mcp.TestSSEClient_ParseEvents_part1`
- `mcp.TestSSEClient_ParseEvents_part2`
- `mcp.TestSSEClient_ParseEvents_part3`
- `mcp.TestSSEClient_Running`
- `mcp.TestSSEClient_StartBackground`
- `mcp.TestSSEClient_StartStop`
- `mcp.TestSSEClient_Stop`
- `mcp.cancel`
- `coverage_test.go`
- `sse_test.go`

### Community 57: agent_forge_test (20 nodes, cohesion=0.050)

- `social.TestAgent_Discover_EmptyForge`
- `social.TestAgent_Handle_Comment_Success`
- `social.TestAgent_Handle_MarkNotificationRead`
- `social.TestAgent_Handle_Mention_Success`
- `social.TestAgent_Handle_Review_Success`
- `social.TestAgent_Handle_UnknownType`
- `social.TestAgent_IsCommentProcessed_MatchingReaction`
- `social.TestAgent_IsCommentProcessed_NoMatch`
- `social.TestAgent_IsCommentProcessed_Plus1`
- `social.TestAgent_IsCommentProcessed_WrongReaction`
- `social.TestAgent_RepoInfo`
- `social.TestAgent_Run_AllItemsSkipped`
- `social.TestAgent_Run_NoItems`
- `social.TestAgent_Run_NotificationProcessed`
- `social.TestAgent_Run_ProcessesAssignment`
- `social.TestAgent_SkillRouter_Initially_Nil`
- `social.TestAgent_WithRegistry_PropagatesSkillRouter`
- `social.TestAgent_WithRegistry_SetsSkillRouter`
- `social.TestClassifyNotification_MentionInTitle`
- `social.newTestAgent`

### Community 58: reviewer_extended_test (20 nodes, cohesion=0.086)

- `reviewer.Agent.reviewChunked_part1`
- `reviewer.Agent.reviewPR_part1`
- `reviewer.Agent.reviewSinglePass_part1`
- `reviewer.ContextBuilder.BuildContext`
- `reviewer.GenerateReport_part1`
- `reviewer.ReportGenerator.FormatFileReview`
- `reviewer.TestCritiqueCode_CriticalSafety_NoLLM`
- `reviewer.TestExtractCriticalIssues`
- `reviewer.TestExtractCriticalIssues_Critical`
- `reviewer.TestExtractCriticalIssues_Empty`
- `reviewer.TestExtractCriticalIssues_HardcodedSecret`
- `reviewer.TestExtractCriticalIssues_Multiple`
- `reviewer.TestExtractCriticalIssues_NoCritical`
- `reviewer.TestExtractCriticalIssues_PathTraversal`
- `reviewer.TestExtractCriticalIssues_RemoteCodeExecution`
- `reviewer.TestExtractCriticalIssues_SQLInjection`
- `reviewer.TestExtractCriticalIssues_SecurityVulnerability`
- `reviewer.extractCriticalIssues`
- `reviewer.len`
- `reviewer_extended_test.go`

### Community 59: loop_adapter (20 nodes, cohesion=0.015)

- `chat.LoopAdapter`
- `chat.LoopAdapter.Agent`
- `chat.LoopAdapter.ChatStreamV2_part2`
- `chat.LoopAdapter.ChatStreamV2_part3`
- `chat.LoopAdapter.WithAgentRegistry`
- `chat.LoopAdapter.WithConfig`
- `chat.LoopAdapter.WithHooks`
- `chat.LoopAdapter.WithMemory`
- `chat.LoopAdapter.WithOnContextUpdate`
- `chat.LoopAdapter.WithPermissions`
- `chat.LoopAdapter.WithToolRegistry`
- `chat.LoopAdapter.WithToolRouter`
- `chat.LoopAdapter.WithTools`
- `chat.LoopAdapter.makeCallModel_part2`
- `chat.LoopAdapter.makeCallModel_part3`
- `chat.LoopAdapter.makeCompactor_part2`
- `chat.LoopAdapter.makeExecuteTools_part2`
- `chat.LoopAdapter.makeExecuteTools_part3`
- `chat.LoopAdapter.makeExecuteTools_part4`
- `loop_adapter.go`

### Community 60: validator_test (20 nodes, cohesion=0.080)

- `configvalidator.New`
- `configvalidator.Result.Valid`
- `configvalidator.TestNew`
- `configvalidator.TestResultInvalid`
- `configvalidator.TestResultValid`
- `configvalidator.TestValidateNeo4jEmpty`
- `configvalidator.TestValidateNeo4jUnreachable`
- `configvalidator.TestValidateOllamaBadStatus`
- `configvalidator.TestValidateOllamaEmpty`
- `configvalidator.TestValidateOllamaSuccess`
- `configvalidator.TestValidateOllamaUnreachable`
- `configvalidator.TestValidatePathsBackupDir`
- `configvalidator.TestValidatePathsBadDir`
- `configvalidator.TestValidatePathsCreateDir`
- `configvalidator.TestValidateVectorDBEmpty`
- `configvalidator.TestValidateVectorDBInvalidProvider`
- `configvalidator.TestValidateVectorDBQdrantUnreachable`
- `configvalidator.Validator.Validate`
- `configvalidator.len`
- `validator_test.go`

### Community 61: chat_view_test (20 nodes, cohesion=0.062)

- `tui.TestChatView_HeightLimiting`
- `tui.TestChatView_Notification`
- `tui.TestChatView_PlanMode`
- `tui.TestChatView_Ready`
- `tui.TestChatView_Streaming`
- `tui.TestChatView_Waiting`
- `tui.TestChatView_WithContextStatus`
- `tui.TestChatView_WithModelName`
- `tui.TestChatView_WithPlanCollapsed`
- `tui.TestChatView_WithPlanSteps`
- `tui.TestChatView_WithSidebar`
- `tui.TestChatView_WithTokenCount`
- `tui.TestChatView_WorkDir`
- `tui.TestRenderMessagesfast_Empty`
- `tui.TestRenderMessagesfast_WithMessages`
- `tui.TestRenderStreamingTail_Empty`
- `tui.TestRenderStreamingTail_WithContent`
- `tui.new`
- `tui.newTestChatModel`
- `chat_view_test.go`

### Community 62: chattools (19 nodes, cohesion=0.016)

- `chattools.Option`
- `chattools.SlashHandler`
- `chattools.SlashHandler.Commands_part1`
- `chattools.SlashHandler.GetPendingImages`
- `chattools.SlashHandler.Handle_part2`
- `chattools.SlashHandler.Handle_part3`
- `chattools.SlashHandler.Handle_part4`
- `chattools.SlashHandler.generateAgentsMD_part2`
- `chattools.SlashHandler.generateAgentsMD_part3`
- `chattools.SlashHandler.handleCommands_part2`
- `chattools.SlashHandler.handleCompact`
- `chattools.SlashHandler.handleImpact_part2`
- `chattools.SlashHandler.handleMode`
- `chattools.SlashHandler.handleNotes_part2`
- `chattools.SlashHandler.handleSettings_part2`
- `chattools.SlashHandler.handleSettings_part3`
- `chattools.SlashResult`
- `chattools.dangerousCommands`
- `chattools.go`

### Community 63: agentloop_test (19 nodes, cohesion=0.097)

- `agentloop.DefaultLoopConfig`
- `agentloop.Run`
- `agentloop.TestInputAwareSafetyClassifier_part2`
- `agentloop.TestRun_Aborted`
- `agentloop.TestRun_CompletesWhenNoToolCalls`
- `agentloop.TestRun_ContextCompressionTriggers`
- `agentloop.TestRun_DeathSpiralDetection`
- `agentloop.TestRun_ErrorRecoveryExhausted`
- `agentloop.TestRun_ErrorRecoveryRetry`
- `agentloop.TestRun_HookPreventsModelCall`
- `agentloop.TestRun_MaxTurns`
- `agentloop.TestRun_OutputTokenEscalation`
- `agentloop.TestRun_PermissionDenied`
- `agentloop.TestRun_PromptTooLongWithCompaction`
- `agentloop.TestRun_StateChange_Callback`
- `agentloop.TestRun_ToolCallLoop`
- `agentloop.cancel`
- `e2e.TestAgentloopE2E_DeathSpiral`
- `agentloop_test.go`

### Community 64: chat (19 nodes, cohesion=0.016)

- `chat.Agent`
- `chat.Agent.Chat`
- `chat.Agent.ChatStream_part2`
- `chat.Agent.ChatStream_part3`
- `chat.Agent.ChatStream_part4`
- `chat.Agent.MessageCount`
- `chat.Agent.SetPlan`
- `chat.Agent.buildContextualPrompt_part2`
- `chat.Agent.buildContextualPrompt_part3`
- `chat.Agent.buildMessages_part2`
- `chat.Agent.buildMessages_part3`
- `chat.Agent.updatePlanFromResponse_part2`
- `chat.Agent.updatePlanFromResponse_part3`
- `chat.ChatMessage`
- `chat.PlanPending`
- `chat.PlanStep`
- `chat.RoleSystem`
- `chat.maxAgenticIterations`
- `chat.go`

### Community 65: hooks_test (19 nodes, cohesion=0.064)

- `hooks.NewRegistry`
- `hooks.TestRegistry_CleanupHookType`
- `hooks.TestRegistry_FirePreA2ACall`
- `hooks.TestRegistry_FirePreFileWrite`
- `hooks.TestRegistry_FirePreTool`
- `hooks.TestRegistry_GuardHookCanPrevent`
- `hooks.TestRegistry_ListHooks`
- `hooks.TestRegistry_NewEvents`
- `hooks.TestRegistry_NoHooksReturnsNil`
- `hooks.TestRegistry_ObserveHookCannotPrevent`
- `hooks.TestRegistry_Prevent`
- `hooks.TestRegistry_PriorityOrder`
- `hooks.TestRegistry_TransformHookCanModify`
- `hooks.TestRegistry_Unregister`
- `hooks.TestSnapshotSecurity_GuardCannotMutateInput`
- `hooks.TestSnapshotSecurity_ObserveCannotMutate`
- `hooks.TestSnapshotSecurity_TransformCanMutate`
- `hooks.string`
- `hooks_test.go`

### Community 66: hooks (18 nodes, cohesion=0.017)

- `hooks.Handler`
- `hooks.HookData`
- `hooks.HookGuard`
- `hooks.HookResult`
- `hooks.HookType`
- `hooks.PreTool_part1`
- `hooks.PreTool_part2`
- `hooks.Registration`
- `hooks.Registry`
- `hooks.Registry.Disable`
- `hooks.Registry.FireError`
- `hooks.Registry.FirePostTool`
- `hooks.Registry.FirePreA2ACall`
- `hooks.Registry.FirePreFileWrite`
- `hooks.Registry.FirePreTool`
- `hooks.Registry.Fire_part2`
- `hooks.Registry.Register`
- `hooks.go`

### Community 67: config (18 nodes, cohesion=0.079)

- `config.Config.Validate_part1`
- `config.SetupWizard.DisplaySummary_part1`
- `config.SetupWizard.LoadExistingConfig`
- `config.TestSplitLines_Empty`
- `config.TestSplitLines_SingleLine`
- `config.TestSplitLines_TrailingNewline`
- `config.TestSplitLines_UnixNewlines`
- `config.TestSplitLines_WindowsNewlines`
- `config.TestValidate_ForgeWarning`
- `config.TrimTrailingSlash`
- `config.ValidationError.Error`
- `config.ValidationError.HasErrors`
- `config.append`
- `config.len`
- `config.loadDotEnv`
- `config.splitLines`
- `validator.go`
- `os.LookupEnv`

### Community 68: onboarding_test (18 nodes, cohesion=0.092)

- `reasoning.TestFindFileContainingSearch`
- `reasoning.TestFindFileContainingSearch_NoMatch`
- `handlers.TestHandleRelease_part1`
- `handlers.TestRetryStateFile_CreatesDirs`
- `handlers.runGit`
- `cmd.TestCheckSEAgent_part1`
- `cmd.TestWriteDocOutput`
- `cmd.checkSEAgent`
- `config.TestLoadExistingConfig_Empty`
- `config.TestLoadExistingEnv`
- `config.TestRun_FullWizard_part1`
- `config.TestSaveEnvFile`
- `dora.run`
- `dora.setupTestRepo_part1`
- `io.NopCloser`
- `os.Environ`
- `os.MkdirTemp`
- `os.RemoveAll`

### Community 69: apiserver_e2e_test (18 nodes, cohesion=0.086)

- `apiserver.New`
- `apiserver.TestServer_DefaultVersion`
- `apiserver.opt`
- `e2e.AssertJSON`
- `e2e.TestAPIServer_404OnUnknownRoute`
- `e2e.TestAPIServer_ConfigEndpoint`
- `e2e.TestAPIServer_HealthEndpoint`
- `e2e.TestAPIServer_HealthzAlias`
- `e2e.TestAPIServer_MetricsEndpoint`
- `e2e.TestAPIServer_ReadyzEndpoint`
- `e2e.TestAPIServer_StatusEndpoint`
- `e2e.TestAPIServer_WebhookEndpoint`
- `e2e.TestWebhook_MethodNotAllowed`
- `e2e.mustJSON`
- `e2e.string`
- `e2e.verifyContent`
- `http.Get`
- `apiserver_e2e_test.go`

### Community 70: manager (18 nodes, cohesion=0.052)

- `mcp.Client.sendNotification`
- `mcp.DiscoveredTool`
- `mcp.Manager`
- `mcp.Manager.AvailableTools`
- `mcp.Manager.ConnectedServers`
- `mcp.Manager.DiscoverTools`
- `mcp.Manager.GetServerForTool`
- `mcp.Manager.GetToolsForLanguage`
- `mcp.Manager.LoadServers`
- `mcp.Manager.StartAll`
- `mcp.Manager.StartServer`
- `mcp.Manager.StopAll`
- `mcp.SSEClient.OnEvent`
- `mcp.ServerConfig`
- `mcp.TestTransportFromString`
- `mcp.append`
- `mcp.transportFromString`
- `manager.go`

### Community 71: gleann_backend (18 nodes, cohesion=0.017)

- `retrieval.AskResult`
- `retrieval.GleannBackend`
- `retrieval.GleannBackend.Available`
- `retrieval.GleannBackendOption`
- `retrieval.HybridSearchRequest`
- `retrieval.SearchIDResult`
- `retrieval.askRequest`
- `retrieval.fetchRequest`
- `retrieval.graphContextAPI`
- `retrieval.multiSearchItem`
- `retrieval.multiSearchRequest`
- `retrieval.multiSearchResponse`
- `retrieval.searchIDsResponse`
- `retrieval.searchRequest`
- `retrieval.searchResponse`
- `retrieval.searchResultItem`
- `retrieval.symbolNeighborAPI`
- `gleann_backend.go`

### Community 72: reader_test (17 nodes, cohesion=0.116)

- `reasoning.NewCodeReader`
- `reasoning.TestCodeReader_ExtractSymbols`
- `reasoning.TestCodeReader_ExtractSymbols_PythonClass`
- `reasoning.TestCodeReader_FindRelevantFiles`
- `reasoning.TestCodeReader_FindRelevantFiles_MaxFiles`
- `reasoning.TestCodeReader_FindRelevantFiles_NoMatch`
- `reasoning.TestCodeReader_GetContextForTask`
- `reasoning.TestCodeReader_GetDirectoryStructure`
- `reasoning.TestCodeReader_GetDirectoryStructure_WithSymbols`
- `reasoning.TestCodeReader_ReadFiles`
- `reasoning.TestCodeReader_ReadFiles_MaxLines`
- `reasoning.TestCodeReader_ReadFiles_NonExistent`
- `reasoning.TestCodeReader_SearchPattern`
- `reasoning.TestCodeReader_SearchPattern_NoMatch`
- `reasoning.TestNewCodeReader`
- `reasoning.setupTestRepo`
- `reader_test.go`

### Community 73: executor_test (17 nodes, cohesion=0.064)

- `coder.NewExecutor`
- `coder.NewRunCommandExecutor`
- `coder.NewTestRunner`
- `coder.QuickFix`
- `coder.TestExecute_ContextCancellation`
- `coder.TestExecute_NilLLM`
- `coder.TestExecutor_ExecuteTool`
- `coder.TestExecutor_MaxAttemptsDefault`
- `coder.TestExecutor_NewExecutorWithMock`
- `coder.TestGetToolDefinitions`
- `coder.TestNewExecutor`
- `coder.TestNewExecutorNilCoverage`
- `coder.TestNewExecutor_CustomExecutors`
- `coder.cancel`
- `cmd.TestExecutorWithMockProvider`
- `executor_di_test.go`
- `executor_test.go`

### Community 74: mode (17 nodes, cohesion=0.018)

- `mode.Actor`
- `mode.Actor.CheckDependencies`
- `mode.Actor.SetCallback`
- `mode.ExecutionPlan`
- `mode.ExecutionPlan.AllDone`
- `mode.ExecutionPlan.HasFailed`
- `mode.ExecutionPlan.Progress`
- `mode.PlanStep`
- `mode.Planner`
- `mode.Planner.Plan`
- `mode.Planner.buildPlanPrompt_part2`
- `mode.StatusPending`
- `mode.StepCallback`
- `mode.StepStatus`
- `mode.actorLLM`
- `mode.plannerLLM`
- `mode.go`

### Community 75: gitlab_errors_test (17 nodes, cohesion=0.059)

- `forge.TestGitLab_CreateIssueComment_BadJSON`
- `forge.TestGitLab_CreateIssue_BadJSON`
- `forge.TestGitLab_CreatePR_BadJSON`
- `forge.TestGitLab_EditIssueComment_BadJSON`
- `forge.TestGitLab_FindPRByBranch_BadJSON`
- `forge.TestGitLab_GetCommentReactions_BadJSON`
- `forge.TestGitLab_GetIssueComments_BadJSON`
- `forge.TestGitLab_GetIssueReactions_BadJSON`
- `forge.TestGitLab_GetIssue_BadJSON`
- `forge.TestGitLab_GetPR_BadJSON`
- `forge.TestGitLab_GetRepo_BadJSON`
- `forge.TestGitLab_ListIssues_BadJSON`
- `forge.TestGitLab_ListNotifications_BadJSON`
- `forge.TestGitLab_ListPRs_BadJSON`
- `forge.TestGitLab_ListRepositories_BadJSON`
- `forge.TestGitLab_SearchIssues_BadJSON`
- `forge.newGitLabBadJSONServer`

### Community 76: search_extended_test (17 nodes, cohesion=0.098)

- `webresearch.TestExtractLinks_Extended`
- `webresearch.TestParseDDGResults_ClassicLinks`
- `webresearch.TestParseDDGResults_DDGInternalLinksSkipped`
- `webresearch.TestParseDDGResults_EmptyHTML`
- `webresearch.TestParseDDGResults_GenericFallback`
- `webresearch.TestParseDDGResults_HTMLInTitle`
- `webresearch.TestParseDDGResults_MaxResults`
- `webresearch.TestParseDDGResults_NoMatchingLinks`
- `webresearch.TestParseDDGResults_NonHTTPLinksSkipped`
- `webresearch.TestParseDDGResults_RedirectURLs`
- `webresearch.TestParseDDGResults_SnippetAttachment`
- `webresearch.TestTruncateStr`
- `webresearch.TestTruncateStr_Extended`
- `webresearch.len`
- `webresearch.parseDDGResults`
- `webresearch.truncateStr`
- `search_extended_test.go`

### Community 77: depth_test (17 nodes, cohesion=0.061)

- `reviewer.NewScratchpad`
- `reviewer.TestNewScratchpad`
- `reviewer.TestScrachpad_MultipleFileReviews`
- `reviewer.TestScratchpadAddFileReviewCoverage`
- `reviewer.TestScratchpadAddFileReviewWithIssuesCoverage`
- `reviewer.TestScratchpadAddSyntaxErrorCoverage`
- `reviewer.TestScratchpadMultipleFindingsCoverage`
- `reviewer.TestScratchpadNilFindingCoverage`
- `reviewer.TestScratchpad_AddFileReview`
- `reviewer.TestScratchpad_AddFileReview_WithIssues`
- `reviewer.TestScratchpad_AddFinding_Critical`
- `reviewer.TestScratchpad_AddFinding_Info`
- `reviewer.TestScratchpad_AddFinding_UpdatesFileState`
- `reviewer.TestScratchpad_AddSyntaxError`
- `reviewer.TestScratchpad_ConcurrentAccess`
- `reviewer.TestScratchpad_CriticalCount`
- `reviewer_coverage_test.go`

### Community 78: github_errors_test (17 nodes, cohesion=0.059)

- `forge.TestGitHub_CreateIssueComment_BadJSON`
- `forge.TestGitHub_CreateIssue_BadJSON`
- `forge.TestGitHub_CreatePR_BadJSON`
- `forge.TestGitHub_EditIssueComment_BadJSON`
- `forge.TestGitHub_FindPRByBranch_BadJSON`
- `forge.TestGitHub_GetCommentReactions_BadJSON`
- `forge.TestGitHub_GetIssueComments_BadJSON`
- `forge.TestGitHub_GetIssueReactions_BadJSON`
- `forge.TestGitHub_GetIssue_BadJSON`
- `forge.TestGitHub_GetPR_BadJSON`
- `forge.TestGitHub_GetRepo_BadJSON`
- `forge.TestGitHub_ListIssues_BadJSON`
- `forge.TestGitHub_ListNotifications_BadJSON`
- `forge.TestGitHub_ListPRs_BadJSON`
- `forge.TestGitHub_ListRepositories_BadJSON`
- `forge.TestGitHub_SearchIssues_BadJSON`
- `forge.newGitHubBadJSONServer`

### Community 79: zombie_test (16 nodes, cohesion=0.102)

- `handlers.FindUnprocessedMentions`
- `handlers.ProgressReporter.render`
- `handlers.SkillRouter.CanRoute`
- `handlers.TestCheckFileVolatility_Empty`
- `handlers.TestCheckFileVolatility_NilLogger`
- `handlers.TestFindUnprocessedMentions`
- `handlers.TestFindUnprocessedMentions_Basic`
- `handlers.TestFindUnprocessedMentions_IgnoreUserApproval`
- `handlers.TestFindUnprocessedMentions_NoMention`
- `handlers.TestFindUnprocessedMentions_SkipAgentComments`
- `handlers.TestFindUnprocessedMentions_SkipCancelled`
- `handlers.TestFindUnprocessedMentions_SkipProcessed`
- `handlers.TestFindUnprocessedMentions_UserCancelled`
- `handlers.len`
- `handlers.socialSession.GetContextSummary`
- `zombie_test.go`

### Community 80: pending_queue_test (16 nodes, cohesion=0.119)

- `social.NewPendingQueueForTest`
- `social.TestPendingQueueAdd`
- `social.TestPendingQueueAddUpdate`
- `social.TestPendingQueueAddWithOptions`
- `social.TestPendingQueueClear`
- `social.TestPendingQueueContains`
- `social.TestPendingQueueGetAllEmpty`
- `social.TestPendingQueueMultipleRepos`
- `social.TestPendingQueuePersistence`
- `social.TestPendingQueuePersistenceFileCreated`
- `social.TestPendingQueueRemove`
- `social.TestPendingQueueRemoveNonExistent`
- `social.WithRepoInfo`
- `social.WithTitle`
- `social.tempQueuePath`
- `pending_queue_test.go`

### Community 81: session_test (16 nodes, cohesion=0.161)

- `session.GetManager`
- `session.ResetManager`
- `session.TestActiveSession`
- `session.TestAddRemoveTag`
- `session.TestCreateSession`
- `session.TestCreateSessionDefaultName`
- `session.TestDeleteSession`
- `session.TestDeleteSession_SwitchesActive`
- `session.TestGetSession_NonExistent`
- `session.TestListSessions`
- `session.TestPersistence`
- `session.TestSearchByTag`
- `session.TestUpdateMetadata`
- `session.cleanup`
- `session.setupTestDir`
- `session_test.go`

### Community 82: loop_test (16 nodes, cohesion=0.070)

- `reasoning.NewReasoningLoop`
- `reasoning.TestApplyHunk`
- `reasoning.TestApplyHunk_StartLineZero`
- `reasoning.TestParseUnifiedDiff`
- `reasoning.TestParseUnifiedDiff_MultipleHunks`
- `reasoning.TestParseUnifiedDiff_NoPath`
- `reasoning.TestReasoningLoop_ApplyChanges_Empty`
- `reasoning.TestReasoningLoop_ApplyChanges_SkipsAbsolutePaths`
- `reasoning.TestReasoningLoop_ExecuteTool`
- `reasoning.TestReasoningLoop_Lesson`
- `reasoning.TestReasoningLoop_Run_EmptyPlan`
- `reasoning.TestReasoningLoop_Run_Success`
- `reasoning.TestReasoningLoop_Run_ThinkError`
- `reasoning.TestReasoningLoop_SetLLMClient`
- `coverage_test.go`
- `loop_test.go`

### Community 83: agent (16 nodes, cohesion=0.019)

- `social.Agent`
- `social.Agent.Run_part2`
- `social.Agent.Run_part3`
- `social.Agent.SetInterval`
- `social.Agent.SkillRouter`
- `social.Agent.discover_part2`
- `social.Agent.discover_part3`
- `social.Agent.discover_part4`
- `social.Agent.discover_part5`
- `social.Agent.discover_part6`
- `social.Agent.discover_part7`
- `social.Agent.discover_part8`
- `social.Agent.handle_part2`
- `social.Agent.handle_part3`
- `social.Agent.repoInfo`
- `agent.go`

### Community 84: onboard (16 nodes, cohesion=0.019)

- `tui.NewOnboard_part2`
- `tui.NewOnboard_part3`
- `tui.OnboardModel`
- `tui.OnboardModel.Init`
- `tui.OnboardModel.Update_part2`
- `tui.OnboardModel.Update_part3`
- `tui.OnboardModel.View_part2`
- `tui.OnboardModel.View_part3`
- `tui.OnboardModel.View_part4`
- `tui.OnboardModel.saveConfig_part2`
- `tui.OnboardModel.saveConfig_part3`
- `tui.OnboardStep`
- `tui.ollamaModelsMsg`
- `tui.openaiModelsMsg`
- `tui.probeResult`
- `onboard.go`

### Community 85: step_executor (16 nodes, cohesion=0.019)

- `coder.PlanStep`
- `coder.ProgressFunc`
- `coder.StepExecutionResult`
- `coder.StepExecutor`
- `coder.StepExecutor.ExecuteSteps_part2`
- `coder.StepExecutor.ExecuteSteps_part3`
- `coder.StepExecutor.ExecuteSteps_part4`
- `coder.StepExecutor.callLLMForStep_part2`
- `coder.StepExecutorOption`
- `coder.StepResult`
- `coder.WithToolRegistry`
- `coder.WithToolRouter`
- `coder.analysisActions`
- `coder.buildStepPrompt_part2`
- `coder.maxToolCallTurns`
- `step_executor.go`

### Community 86: handler (16 nodes, cohesion=0.019)

- `coder.BranchManager`
- `coder.BranchManager.Fetch`
- `coder.BranchManager.ForceCleanup`
- `coder.BranchManager.SwitchOrCreateBranch_part2`
- `coder.CodingTaskConfig`
- `coder.CodingTaskResult`
- `coder.HandleCodingTask_part2`
- `coder.HandleCodingTask_part3`
- `coder.HandleCodingTask_part4`
- `coder.HandleCodingTask_part5`
- `coder.HandleCodingTask_part6`
- `coder.HandleCodingTask_part7`
- `coder.PhaseBranch`
- `coder.ProgressCallback`
- `coder.ProgressPhase`
- `handler.go`

### Community 87: gleann_e2e_test (16 nodes, cohesion=0.100)

- `e2e.AssertContains`
- `e2e.TestGleannBackend_Ask`
- `e2e.TestGleannBackend_Available`
- `e2e.TestGleannBackend_GraphQuery`
- `e2e.TestGleannBackend_HybridSearch`
- `e2e.TestGleannBackend_MultiIndexSearch`
- `e2e.TestGleannBackend_Unavailable`
- `e2e.TestGleannBackend_VectorSearch`
- `e2e.TestGleannBackend_WirePipeline`
- `e2e.TestGleann_LiveSearch`
- `e2e.TestGleann_ProgressiveDisclosure`
- `e2e.TestGleann_ProgressiveDisclosure_Fallback`
- `e2e.mockGleannFullPipelineServer_part2`
- `e2e.mockGleannSearchServer`
- `e2e.queryFn`
- `gleann_e2e_test.go`

### Community 88: collector_test (16 nodes, cohesion=0.068)

- `metrics.Collector.Clear`
- `metrics.Collector.Get`
- `metrics.Collector.load`
- `metrics.TestCollectorPersistence`
- `metrics.TestCollectorRecordHandler`
- `metrics.TestCollectorRecordLLMCall`
- `metrics.TestCollector_CriticalIssueCounting`
- `metrics.TestCollector_RecordSelfAuditWithDORA`
- `metrics.TestCollector_RecordSelfAudit_AvgScore`
- `metrics.TestCollector_RecordSelfAudit_DORALink_HighScore`
- `metrics.TestCollector_RecordSelfAudit_DORALink_LowScore`
- `metrics.TestCollector_RecordSelfAudit_IncrementsStats`
- `metrics.TestCollector_RecordSelfAudit_NoDORAState`
- `metrics.TestHandlerStatsAvgDuration`
- `metrics.make`
- `collector_test.go`

### Community 89: e2e (16 nodes, cohesion=0.121)

- `e2e.MockA2AServer_part1`
- `e2e.MockForgeServer_part1`
- `e2e.TestWebhookSocial_IssueOpened_Chain_part1`
- `e2e.TestWebhookSocial_MultipleEvents_part1`
- `e2e.TestWebhookSocial_PRComment_Chain_part1`
- `e2e.TestWebhook_IssueOpened`
- `e2e.append`
- `e2e.copy`
- `e2e.e2eMCPCaller.AvailableTools`
- `e2e.extractIssueNum`
- `e2e.len`
- `e2e.make`
- `e2e.mockOllama.handleChat`
- `e2e.mockOllama.snapshot`
- `e2e.truncate`
- `e2e.webhookEventToSocialItem`

### Community 90: working_test (16 nodes, cohesion=0.097)

- `chat.Agent.SetRepoPath`
- `memory.ConversationStore.GetContextString`
- `memory.NewWorkingMemory`
- `memory.TestSession_MarshalJSON`
- `memory.TestWorkingMemory_GetContext`
- `memory.TestWorkingMemory_New`
- `memory.TestWorkingMemory_PinFile`
- `memory.TestWorkingMemory_PinFile_NotExists`
- `memory.TestWorkingMemory_ReadFileTruncation`
- `memory.TestWorkingMemory_UnpinFile`
- `memory.WorkingMemory.GetContext`
- `memory.contains`
- `memory.containsSubstring`
- `memory.len`
- `memory.make`
- `working_test.go`

### Community 91: depth_test (15 nodes, cohesion=0.079)

- `reviewer.CheckSafety`
- `reviewer.TestCheckSafety_AllPatterns_part1`
- `reviewer.TestCheckSafety_Chmod777`
- `reviewer.TestCheckSafety_CleanCode`
- `reviewer.TestCheckSafety_CloudCreds`
- `reviewer.TestCheckSafety_Eval`
- `reviewer.TestCheckSafety_HardcodedCredential`
- `reviewer.TestCheckSafety_InnerHTML`
- `reviewer.TestCheckSafety_LineNumbers`
- `reviewer.TestCheckSafety_PrivateKey`
- `reviewer.TestCheckSafety_SQLInjection`
- `reviewer.TestCheckSafety_SSLVerifyDisabled`
- `reviewer.TestCheckSafety_TODO`
- `reviewer.TestReviewFilesBatch_SafetyOnly`
- `depth_test.go`

### Community 92: pipeline_test (15 nodes, cohesion=0.114)

- `coder.BuildAugmentedContext`
- `coder.TestBuildAugmentedContext_WithPipeline`
- `retrieval.DefaultConfig`
- `retrieval.NewPipeline`
- `retrieval.TestDefaultConfig`
- `retrieval.TestPipeline_EmptyBackends`
- `retrieval.TestPipeline_GetContextForAgent`
- `retrieval.TestPipeline_GetContext_Basic`
- `retrieval.TestPipeline_GetContext_Confidence`
- `retrieval.TestPipeline_GetContext_DisabledBackends`
- `retrieval.TestPipeline_GetContext_SessionHistory`
- `retrieval.TestPipeline_GetContext_WithBackends`
- `retrieval.WirePipelineFull`
- `retrieval.WithSessionHistory`
- `retrieval.WithTaskDescription`

### Community 93: helpers_test (15 nodes, cohesion=0.089)

- `handlers.ExtractPhaseContext`
- `handlers.TestCheckFileVolatility_WithMock`
- `handlers.TestExtractPhaseContext_ItemReversePattern`
- `handlers.TestExtractPhaseContext_NoHistory`
- `handlers.TestExtractPhaseContext_NoMatch`
- `handlers.TestExtractPhaseContext_NoReference`
- `handlers.TestExtractPhaseContext_PhaseAndItem`
- `handlers.TestExtractPhaseContext_PhaseOnly`
- `handlers.TestExtractPhaseContext_PriorityMapping`
- `handlers.TestExtractPhaseContext_Turkish`
- `handlers.contains`
- `handlers.containsStr`
- `handlers.mockForgeHelper`
- `handlers.mockForgeHelper.GetCommentReactions`
- `helpers_test.go`

### Community 94: lifecycle (15 nodes, cohesion=0.020)

- `planner.PlanCreated`
- `planner.PlanProgress`
- `planner.PlanRevision`
- `planner.PlanStatus`
- `planner.PlanTracker`
- `planner.PlanTracker.Get`
- `planner.StepPending`
- `planner.StepStatus`
- `planner.TrackedPlan`
- `planner.TrackedPlan.NextPendingStep`
- `planner.TrackedPlan.allStepsTerminal`
- `planner.TrackedPlan.notifyPlanChange`
- `planner.TrackedPlan.notifyStepChange`
- `planner.TrackedStep`
- `lifecycle.go`

### Community 95: chat (15 nodes, cohesion=0.067)

- `tui.BenchmarkStreamingAnimation`
- `tui.ChatModel.autoCheckProgress_part1`
- `tui.ChatModel.chromeHeight`
- `tui.ChatModel.renderMessages`
- `tui.ChatModel.renderMessagesfast`
- `tui.ChatModel.renderNotification`
- `tui.ConfigSetModel.Update`
- `tui.MenuModel.Update`
- `tui.OnboardModel.skipIrrelevant`
- `tui.TestNewChatDefaults`
- `tui.TestTrackChangedFiles`
- `tui.autocompleteState.moveDown`
- `tui.autocompleteState.selected`
- `tui.autocompleteState.view`
- `tui.len`

### Community 96: depth_extended_test (15 nodes, cohesion=0.067)

- `reviewer.GenerateReport`
- `reviewer.TestGenerateReport_CrossFileDepsSection`
- `reviewer.TestGenerateReport_EmptyReport`
- `reviewer.TestGenerateReport_EmptyScratchpad`
- `reviewer.TestGenerateReport_FileReviewStatus`
- `reviewer.TestGenerateReport_FileReviewsSection`
- `reviewer.TestGenerateReport_LongReviewTruncation`
- `reviewer.TestGenerateReport_Minimal`
- `reviewer.TestGenerateReport_OKFileReview`
- `reviewer.TestGenerateReport_SyntaxErrorSection`
- `reviewer.TestGenerateReport_WithCriticalsAndWarnings`
- `reviewer.TestGenerateReport_WithFindings`
- `reviewer.TestGenerateReport_WithSyntaxErrors`
- `reviewer.TestReviewDecision_Values`
- `depth_extended_test.go`

### Community 97: serve_code_task (15 nodes, cohesion=0.020)

- `cmd.applyAtomicEdit_part2`
- `cmd.applyAtomicEdit_part3`
- `cmd.applyDiffPlan_part2`
- `cmd.codeTaskEdit`
- `cmd.codeTaskPlan`
- `cmd.diffBasedCodeTaskEdit`
- `cmd.diffBasedCodeTaskPlan`
- `cmd.discoverTargetFiles_part2`
- `cmd.discoverTargetFiles_part3`
- `cmd.discoverTargetFiles_part4`
- `cmd.executeCodeTaskAtomic_part2`
- `cmd.executeCodeTaskAtomic_part3`
- `cmd.executeCodeTaskAtomic_part4`
- `cmd.tryParseJSON_part2`
- `serve_code_task.go`

### Community 98: cmd_utils_test (15 nodes, cohesion=0.065)

- `cmd.TestBenchReport_Fields`
- `cmd.TestBenchTask_Fields`
- `cmd.TestFilterBenchTasks_All`
- `cmd.TestFilterBenchTasks_AllCategory`
- `cmd.TestFilterBenchTasks_BugFix`
- `cmd.TestFilterBenchTasks_ByDifficulty`
- `cmd.TestFilterBenchTasks_ByType`
- `cmd.TestFilterBenchTasks_Easy`
- `cmd.TestFilterBenchTasks_Medium`
- `cmd.TestFilterBenchTasks_NoMatch`
- `cmd.TestFilterBenchTasks_NoMatchCategory`
- `cmd.TestFilterBenchTasks_Refactor`
- `cmd.filterBenchTasks`
- `benchmark_extended_test.go`
- `cmd_utils_test.go`

### Community 99: manager (15 nodes, cohesion=0.025)

- `indexmanager.IndexConfig`
- `indexmanager.Manager`
- `indexmanager.Manager.AddSharedIndex`
- `indexmanager.Manager.Config`
- `indexmanager.Manager.Register`
- `indexmanager.Manager.UnlinkSharedIndex`
- `indexmanager.RepoIndex`
- `indexmanager.ResolveResult`
- `indexmanager.ResolveSource`
- `indexmanager.ResolveSourceExplicitEnv`
- `indexmanager.SharedIndex`
- `indexmanager.gitConfigRe`
- `indexmanager.instance`
- `indexmanager.removeStr`
- `manager.go`

### Community 100: depth (15 nodes, cohesion=0.020)

- `reviewer.Critic`
- `reviewer.Critic.CritiqueCode_part2`
- `reviewer.CriticResult`
- `reviewer.FileReviewState`
- `reviewer.Finding`
- `reviewer.GenerateReport_part2`
- `reviewer.GenerateReport_part3`
- `reviewer.SafetyIssue`
- `reviewer.Scratchpad`
- `reviewer.Scratchpad.AddFileReview`
- `reviewer.Scratchpad.CriticalCount`
- `reviewer.batchSize`
- `reviewer.defaultSafetyPatterns_part1`
- `reviewer.safetyPattern`
- `depth.go`

### Community 101: registry (15 nodes, cohesion=0.020)

- `tools.Param`
- `tools.Registry`
- `tools.Registry.Has`
- `tools.Registry.Register`
- `tools.Registry.registerDefaults_part2`
- `tools.Registry.registerDefaults_part3`
- `tools.Registry.registerDefaults_part4`
- `tools.Registry.registerDefaults_part5`
- `tools.Registry.registerDefaults_part6`
- `tools.Registry.registerDefaults_part7`
- `tools.Registry.registerDefaults_part8`
- `tools.RegistryOption`
- `tools.ToolFunc`
- `tools.ToolInfo`
- `registry.go`

### Community 102: registry_test (15 nodes, cohesion=0.114)

- `agentregistry.New`
- `agentregistry.Registry.Summary`
- `agentregistry.TestRegistry_AvailableAgents`
- `agentregistry.TestRegistry_CallSkill`
- `agentregistry.TestRegistry_CallSkill_Unknown`
- `agentregistry.TestRegistry_DiscoverAgent`
- `agentregistry.TestRegistry_HealthCheck`
- `agentregistry.TestRegistry_HealthCheck_Unavailable`
- `agentregistry.TestRegistry_Refresh`
- `agentregistry.TestRegistry_StartHealthMonitor`
- `agentregistry.TestRegistry_Summary`
- `agentregistry.TestSkillCallerAdapter_GetSkill_NotFound`
- `agentregistry.len`
- `agentregistry.mockA2AServer`
- `registry_test.go`

### Community 103: progress_test (15 nodes, cohesion=0.114)

- `handlers.NewProgressReporter`
- `handlers.TestNewProgressReporter`
- `handlers.TestProgressReporter_AddPhase`
- `handlers.TestProgressReporter_Construct`
- `handlers.TestProgressReporter_Disable`
- `handlers.TestProgressReporter_Duration_Formatting`
- `handlers.TestProgressReporter_Finish_Failure`
- `handlers.TestProgressReporter_Finish_Idempotent`
- `handlers.TestProgressReporter_Finish_Success`
- `handlers.TestProgressReporter_PostPlan`
- `handlers.TestProgressReporter_PostPlan_NoSteps`
- `handlers.TestProgressReporter_Render_CheckboxStates`
- `handlers.TestProgressReporter_UpdateStep`
- `handlers.makeProgressCallback_part1`
- `handlers.newMockForgeProgress`

### Community 104: conversation (15 nodes, cohesion=0.020)

- `memory.Conversation`
- `memory.ConversationStore`
- `memory.ConversationStore.AddMessage`
- `memory.ConversationStore.Clear`
- `memory.ConversationStore.Close`
- `memory.ConversationStore.Count`
- `memory.ConversationStore.migrate_part1`
- `memory.ConversationStore.migrate_part2`
- `memory.Message`
- `memory.Session`
- `memory.Session.AddAssistantMessage`
- `memory.Session.AddHumanMessage`
- `memory.Session.Close`
- `memory.Session.GetContext`
- `conversation.go`

### Community 105: permissions (15 nodes, cohesion=0.020)

- `permissions.Allow`
- `permissions.AuditEntry`
- `permissions.CatReadOnly`
- `permissions.Decision`
- `permissions.Mode.String`
- `permissions.ModeDefault`
- `permissions.PermissionPolicy`
- `permissions.Service`
- `permissions.Service.GetMode`
- `permissions.Service.SetCustomPolicy`
- `permissions.Service.SetMode`
- `permissions.Service.checkDefault`
- `permissions.ToolCategory`
- `permissions.ToolMeta`
- `permissions.go`

### Community 106: a2a_test (14 nodes, cohesion=0.167)

- `a2a.DefaultYaverCard`
- `a2a.NewServer`
- `a2a.TestDefaultYaverCard`
- `a2a.TestServer_AgentCard`
- `a2a.TestServer_GetTask`
- `a2a.TestServer_GetTask_NotFound`
- `a2a.TestServer_SendMessage`
- `a2a.TestServer_SendMessage_EmptyMessage`
- `a2a.TestServer_SendMessage_SkillNotFound`
- `a2a.TestServer_SkillRouting`
- `a2a.len`
- `e2e.TestA2AIntegrationE2E_part1`
- `a2a_test.go`
- `http.NewServeMux`

### Community 107: pipeline (14 nodes, cohesion=0.021)

- `retrieval.BackendQueryFunc`
- `retrieval.ContextPriority`
- `retrieval.ContextPriority.String`
- `retrieval.ContextSection`
- `retrieval.GetContextOption`
- `retrieval.Pipeline`
- `retrieval.Pipeline.GetContext_part2`
- `retrieval.Pipeline.GetContext_part3`
- `retrieval.Pipeline.GetContext_part4`
- `retrieval.PriorityLow`
- `retrieval.RetrievalConfig`
- `retrieval.RetrievalResult`
- `retrieval.getContextOpts`
- `pipeline.go`

### Community 108: dashboard_test (14 nodes, cohesion=0.134)

- `tui.DashboardLogHandler.Handle`
- `tui.NewDashboard`
- `tui.NewOnboard_part1`
- `tui.TestDashboardEvent_Fields`
- `tui.TestDashboardPhaseConstants`
- `tui.TestDashboardView_Daemon`
- `tui.TestDashboardView_Idle`
- `tui.TestDashboardView_Quitting`
- `tui.TestDashboardView_WithTask`
- `tui.TestNewDashboard`
- `tui.TestNewDashboard_Daemon`
- `tui.close`
- `tui.make`
- `dashboard_test.go`

### Community 109: mode_test (14 nodes, cohesion=0.021)

- `mode.TestActor_CheckDependencies_DepsMet`
- `mode.TestActor_CheckDependencies_DepsNotMet`
- `mode.TestActor_CheckDependencies_DepsSkipped`
- `mode.TestActor_CheckDependencies_NoDeps`
- `mode.TestExecutionPlan_AllDone`
- `mode.TestExecutionPlan_HasFailed`
- `mode.TestExecutionPlan_Progress`
- `mode.TestExecutionPlan_TotalSteps`
- `mode.mockActorLLM`
- `mode.mockActorLLM.Generate`
- `mode.mockActorLLM.InvokeWithRetry`
- `mode.mockPlannerLLM`
- `mode.mockPlannerLLM.Generate`
- `mode_test.go`

### Community 110: cache (14 nodes, cohesion=0.021)

- `prompt.BoundaryPrefix`
- `prompt.CacheAwareBuilder`
- `prompt.SectionCache`
- `prompt.SectionCache.Clear`
- `prompt.SectionCache.Invalidate`
- `prompt.SectionCache.RenderCached`
- `prompt.SectionCache.Size`
- `prompt.SectionSpec`
- `prompt.StickyLatch`
- `prompt.StickyLatch.Include`
- `prompt.StickyLatch.IsActive`
- `prompt.StickyLatch.ShouldInclude`
- `prompt.cacheEntry`
- `cache.go`

### Community 111: mcpserver (14 nodes, cohesion=0.021)

- `mcpserver.DefaultTools_part1`
- `mcpserver.DefaultTools_part2`
- `mcpserver.DefaultTools_part3`
- `mcpserver.DefaultTools_part4`
- `mcpserver.JSONSchema`
- `mcpserver.Property`
- `mcpserver.Server`
- `mcpserver.Server.Mount`
- `mcpserver.Tool`
- `mcpserver.ToolHandler`
- `mcpserver.toolCallRequest`
- `mcpserver.toolCallResponse`
- `mcpserver.toolContent`
- `mcpserver.go`

### Community 112: chat (14 nodes, cohesion=0.021)

- `cmd.chatCmd`
- `cmd.chatSession`
- `cmd.runChat_part10`
- `cmd.runChat_part11`
- `cmd.runChat_part2`
- `cmd.runChat_part3`
- `cmd.runChat_part4`
- `cmd.runChat_part5`
- `cmd.runChat_part6`
- `cmd.runChat_part7`
- `cmd.runChat_part8`
- `cmd.runChat_part9`
- `cmd.runHeadless_part2`
- `chat.go`

### Community 113: config_extended_test (13 nodes, cohesion=0.023)

- `config.TestDefaultGitCommitEmail`
- `config.TestDefaultGitCommitName`
- `config.TestModelFor_OllamaCode`
- `config.TestModelFor_OllamaDefault`
- `config.TestModelFor_OllamaExtraction`
- `config.TestModelFor_OllamaReasoning`
- `config.TestModelFor_OpenAICode`
- `config.TestModelFor_OpenAIDefault`
- `config.TestModelFor_OpenAIExtraction`
- `config.TestModelFor_OpenAIReasoning`
- `config.TestValidate_NoErrors`
- `config.TestValidate_WebhookWarning`
- `config_extended_test.go`

### Community 114: sdd (13 nodes, cohesion=0.023)

- `cmd.PhaseConOps`
- `cmd.PhaseStatePending`
- `cmd.SDDPhase`
- `cmd.SDDPhaseState`
- `cmd.SDDResult`
- `cmd.SDDWorkflowState`
- `cmd.runSDD_part2`
- `cmd.runSDD_part3`
- `cmd.runSDD_part4`
- `cmd.runSDD_part5`
- `cmd.sddCmd`
- `cmd.sddRepo`
- `sdd.go`

### Community 115: se_agent_e2e_test (13 nodes, cohesion=0.122)

- `a2a.NewClient`
- `e2e.TestSEAgent_AuditTrace`
- `e2e.TestSEAgent_Available`
- `e2e.TestSEAgent_Discovery`
- `e2e.TestSEAgent_FullPipeline_part1`
- `e2e.TestSEAgent_FullPipeline_part2`
- `e2e.TestSEAgent_GetTask`
- `e2e.TestSEAgent_ImpactAnalysis`
- `e2e.TestSEAgent_RequirementQuery`
- `e2e.TestSEAgent_TaskExecution`
- `e2e.mockSEAgentServer`
- `e2e.mockSEAgentServer_part2`
- `se_agent_e2e_test.go`

### Community 116: gitea_errors_test (13 nodes, cohesion=0.077)

- `forge.TestGitea_FindPRByBranch_BadJSON`
- `forge.TestGitea_GetCommentReactions_BadJSON`
- `forge.TestGitea_GetIssueComments_BadJSON`
- `forge.TestGitea_GetIssueReactions_BadJSON`
- `forge.TestGitea_GetIssue_BadJSON`
- `forge.TestGitea_GetPR_BadJSON`
- `forge.TestGitea_GetRepo_BadJSON`
- `forge.TestGitea_ListIssues_BadJSON`
- `forge.TestGitea_ListNotifications_BadJSON`
- `forge.TestGitea_ListPRs_BadJSON`
- `forge.TestGitea_ListRepositories_BadJSON`
- `forge.TestGitea_SearchIssues_BadJSON`
- `forge.newGiteaBadJSONServer`

### Community 117: session (13 nodes, cohesion=0.023)

- `session.FusedResult`
- `session.Manager`
- `session.Manager.GetSession`
- `session.Manager.ListSessions`
- `session.Manager.UpdateMetadata`
- `session.QueryResult`
- `session.QuerySemantic`
- `session.QueryType`
- `session.Session`
- `session.sessionDir`
- `session.sessionStore`
- `session.singleton`
- `session.go`

### Community 118: chat_helpers_test (13 nodes, cohesion=0.088)

- `tui.BenchmarkExtractPlanSteps`
- `tui.TestExtractPlanSteps`
- `tui.TestExtractPlanSteps_CheckmarkVariants`
- `tui.TestExtractPlanSteps_DoubleDigit`
- `tui.TestExtractPlanSteps_HeaderFormat`
- `tui.TestExtractPlanSteps_MaxTen`
- `tui.TestExtractPlanSteps_NumberedList`
- `tui.TestExtractPlanSteps_ParenFormat`
- `tui.TestExtractPlanSteps_TooFew`
- `tui.TestExtractPlanSteps_TruncatesLong`
- `tui.TestExtractPlanSteps_WithDoneMarkers`
- `tui.extractPlanSteps`
- `chat_helpers_test.go`

### Community 119: tools (13 nodes, cohesion=0.023)

- `coder.BaseTool`
- `coder.BaseTool.Description`
- `coder.BaseTool.Execute`
- `coder.BaseTool.Name`
- `coder.BaseTool.Parameters`
- `coder.ListDirExecutor`
- `coder.ReadFileExecutor`
- `coder.RunCommandExecutor`
- `coder.RunCommandExecutor.Execute_part2`
- `coder.RunCommandExecutor.Execute_part3`
- `coder.ToolExecutor`
- `coder.WebSearchExecutor`
- `tools.go`

### Community 120: menu_autocomplete_test (13 nodes, cohesion=0.079)

- `tui.BenchmarkAutocompleteUpdate`
- `tui.TestAutocompleteFilter`
- `tui.TestAutocompleteNavigation`
- `tui.TestAutocomplete_Accept`
- `tui.TestAutocomplete_Dismiss`
- `tui.TestAutocomplete_MoveUpDown`
- `tui.TestAutocomplete_Selected`
- `tui.TestAutocomplete_View`
- `tui.TestAutocomplete_View_Hidden`
- `tui.defaultSlashCommands`
- `tui.newAutocomplete`
- `chat_test.go`
- `menu_autocomplete_test.go`

### Community 121: webhook (13 nodes, cohesion=0.023)

- `webhook.Event`
- `webhook.EventIssueOpened`
- `webhook.EventType`
- `webhook.Handler`
- `webhook.Option`
- `webhook.Receiver`
- `webhook.Receiver.HTTPHandler`
- `webhook.Receiver.classifyIssueEvent`
- `webhook.Receiver.classifyPREvent`
- `webhook.Receiver.parseEvent_part2`
- `webhook.Receiver.parseEvent_part3`
- `webhook.RepoRef`
- `webhook.go`

### Community 122: status_dashboard_test (13 nodes, cohesion=0.131)

- `bytes.Contains`
- `cmd.TestPrintSummary_AllReady`
- `cmd.TestPrintSummary_WithErrors`
- `cmd.TestRenderDashboard_IdleState`
- `cmd.TestRenderDashboard_NoIssuesWhenClean`
- `cmd.TestRenderDashboard_RendersExpectedSections`
- `cmd.TestRenderDashboard_WithBottlenecksAndErrors`
- `cmd.TestRunDoctor_NoLLM`
- `cmd.renderDashboard`
- `cmd.runDoctor`
- `doctor_flags_test.go`
- `status_dashboard_test.go`
- `os.Pipe`

### Community 123: reviewer (13 nodes, cohesion=0.023)

- `reviewer.Agent`
- `reviewer.Agent.ReviewRepo_part2`
- `reviewer.Agent.WithCommunityClient`
- `reviewer.Agent.WithPipeline`
- `reviewer.Agent.reviewPR_part2`
- `reviewer.Agent.reviewPR_part3`
- `reviewer.Agent.reviewSinglePass_part2`
- `reviewer.Agent.reviewSinglePass_part3`
- `reviewer.Agent.reviewSingle_part2`
- `reviewer.Approved`
- `reviewer.ReviewDecision`
- `reviewer.ReviewResult`
- `reviewer.go`

### Community 124: bootstrap (13 nodes, cohesion=0.041)

- `cmd.TestStatusIcon`
- `cmd.TestStatusIcon_EdgeCases`
- `cmd.TestStatusIcon_False`
- `cmd.TestStatusIcon_True`
- `cmd.bootstrapper`
- `cmd.bootstrapper.Run_part2`
- `cmd.bootstrapper.Run_part3`
- `cmd.bootstrapper.checkAndPullModels_part2`
- `cmd.bootstrapper.checkAndPullModels_part3`
- `cmd.serviceStatus`
- `cmd.statusIcon`
- `cmd.systemBootstrapCmd`
- `bootstrap.go`

### Community 125: mcp_test (13 nodes, cohesion=0.128)

- `mcp.DefaultConfig`
- `mcp.NewClient`
- `mcp.TestCallToolNotConnected`
- `mcp.TestConnectHTTPNoURL`
- `mcp.TestConnectStdioNoCommand`
- `mcp.TestConnectUnsupportedTransport`
- `mcp.TestDefaultConfig`
- `mcp.TestDisconnectNotConnected`
- `mcp.TestEnableSSE_RequiresHTTP`
- `mcp.TestListResourcesNotConnected`
- `mcp.TestListToolsNotConnected`
- `mcp.TestNewClient`
- `mcp.isConnectionError`

### Community 126: auditor (13 nodes, cohesion=0.053)

- `selfaudit.Auditor`
- `selfaudit.Auditor.AuditAnswer_part1`
- `selfaudit.Auditor.AuditAnswer_part2`
- `selfaudit.Auditor.AuditCodeChange`
- `selfaudit.Auditor.AuditPatch_part1`
- `selfaudit.Auditor.AuditPatch_part2`
- `selfaudit.Issue`
- `selfaudit.Result`
- `selfaudit.Thresholds`
- `selfaudit.clamp`
- `selfaudit.countComplexity`
- `selfaudit.float64`
- `auditor.go`

### Community 127: conversation_extended_test (12 nodes, cohesion=0.078)

- `memory.TestConversationStore_AddAndGetHistory`
- `memory.TestConversationStore_Clear`
- `memory.TestConversationStore_Count`
- `memory.TestConversationStore_GetContextString`
- `memory.TestConversationStore_GetContextString_Empty`
- `memory.TestConversationStore_GetHistory_Empty`
- `memory.TestConversationStore_IsolatedIssues`
- `memory.TestSession_Close_NilConversation`
- `memory.TestSession_FullName`
- `memory.TestSession_GetContext_NilConversation`
- `memory.newTestCS`
- `conversation_extended_test.go`

### Community 128: tri_agent_integration_e2e_test (12 nodes, cohesion=0.025)

- `e2e.TestAgentRegistryE2E_DiscoverAndCallSkill_part2`
- `e2e.TestTriAgentE2E_FullPipeline_part10`
- `e2e.TestTriAgentE2E_FullPipeline_part2`
- `e2e.TestTriAgentE2E_FullPipeline_part3`
- `e2e.TestTriAgentE2E_FullPipeline_part4`
- `e2e.TestTriAgentE2E_FullPipeline_part5`
- `e2e.TestTriAgentE2E_FullPipeline_part6`
- `e2e.TestTriAgentE2E_FullPipeline_part7`
- `e2e.TestTriAgentE2E_FullPipeline_part8`
- `e2e.TestTriAgentE2E_FullPipeline_part9`
- `e2e.e2eMCPCaller`
- `tri_agent_integration_e2e_test.go`

### Community 129: benchmark (12 nodes, cohesion=0.025)

- `cmd.benchCategory`
- `cmd.benchReport`
- `cmd.benchResult`
- `cmd.benchTask`
- `cmd.benchmarkCmd`
- `cmd.benchmarkCompareCmd`
- `cmd.benchmarkListCmd`
- `cmd.benchmarkRunCmd`
- `cmd.defaultBenchTasks`
- `cmd.runBenchmarkRun_part2`
- `cmd.runBenchmarkRun_part3`
- `benchmark.go`

### Community 130: recall (12 nodes, cohesion=0.025)

- `gleannmemory.AnnotatedBlock`
- `gleannmemory.BackgroundExtractor`
- `gleannmemory.BackgroundExtractor.Running`
- `gleannmemory.BackgroundExtractor.Stop`
- `gleannmemory.ExtractionConfig`
- `gleannmemory.ExtractionFn`
- `gleannmemory.MemoryBlocks.SmartRecall_part2`
- `gleannmemory.MemoryType`
- `gleannmemory.MemoryTypeUser`
- `gleannmemory.RecallFn`
- `gleannmemory.RecallResult`
- `recall.go`

### Community 131: chat_coverage_test (12 nodes, cohesion=0.101)

- `chat.TestAgent_CatHWithMeta_ProviderError`
- `chat.TestAgent_ChatStream_Basic`
- `chat.TestAgent_ChatStream_ProviderError`
- `chat.TestAgent_Chat_HistoryPreserved`
- `chat.TestAgent_Chat_ProviderError`
- `chat.TestAgent_InjectMemoryContext`
- `chat.TestAgent_InjectMemoryContext_Empty`
- `chat.TestAgent_InjectToolDescriptions`
- `chat.TestAgent_SetRepoPath`
- `chat.TestLoopAdapter_New`
- `chat.makeChatAgent`
- `chat_coverage_test.go`

### Community 132: reflector_test (12 nodes, cohesion=0.098)

- `reasoning.NewReflector`
- `reasoning.TestReflector_ClassifyTask`
- `reasoning.TestReflector_GetRecommendedApproach`
- `reasoning.TestReflector_GetRecommendedApproach_NoMatch`
- `reasoning.TestReflector_GetWarnings`
- `reasoning.TestReflector_GetWarnings_NoMatch`
- `reasoning.TestReflector_Reflect_Success`
- `reasoning.TestReflector_Reflect_Template`
- `reasoning.TestReflector_Reflect_WithLLM`
- `reasoning.TestReflector_Summary`
- `reasoning.TestReflector_WarningsLimit`
- `reflector_test.go`

### Community 133: codeparse_test (12 nodes, cohesion=0.135)

- `codeparse.Analyze_part1`
- `codeparse.TestAnalyze_GoSource`
- `codeparse.TestParseFile_GoSource`
- `codeparse.TestParseFunctions_GoSource`
- `codeparse.TestParseImportBlock_Empty`
- `codeparse.TestParseImportBlock_Go`
- `codeparse.TestParseImportBlock_Python`
- `codeparse.append`
- `codeparse.len`
- `codeparse.make`
- `codeparse.parseImportBlock`
- `codeparse_test.go`

### Community 134: clarifier (12 nodes, cohesion=0.025)

- `clarifier.AmbiguityClear`
- `clarifier.ClarificationResult`
- `clarifier.ClarificationResult.FormatIssueComment_part2`
- `clarifier.Clarifier`
- `clarifier.Clarifier.AnalyzeRequest`
- `clarifier.Clarifier.parseResponse_part2`
- `clarifier.Clarifier.parseResponse_part3`
- `clarifier.ClarifyingQuestion`
- `clarifier.Config`
- `clarifier.ImplementationPath`
- `clarifier.ambiguityRe`
- `clarifier.go`

### Community 135: history (12 nodes, cohesion=0.025)

- `history.AnalysisEntry`
- `history.AnalysisRecord`
- `history.Manager`
- `history.Manager.ClearAll`
- `history.Manager.Close`
- `history.Manager.DeleteProject`
- `history.Manager.GetLastAnalysis`
- `history.Manager.ShouldReanalyze`
- `history.ProjectMeta`
- `history.ReanalyzeReason`
- `history.ReasonFirstTime`
- `history.go`

### Community 136: serve_code_task_test (12 nodes, cohesion=0.025)

- `cmd.TestDiscoverTargetFiles_part2`
- `cmd.TestDiscoverTargetFiles_part3`
- `cmd.TestExtractJSONFromProse_part2`
- `cmd.TestExtractJSONFromProse_part3`
- `cmd.TestExtractRelevantRegion_part2`
- `cmd.TestExtractRelevantRegion_part3`
- `cmd.TestFindOriginalByLines_part2`
- `cmd.TestFindOriginalByLines_part3`
- `cmd.TestFindPartialMatch_part2`
- `cmd.TestTryParseJSON_part2`
- `cmd.TestTryParseJSON_part3`
- `serve_code_task_test.go`

### Community 137: self_review (12 nodes, cohesion=0.025)

- `coder.ReviewFinding`
- `coder.ReviewResult`
- `coder.SelfReview`
- `coder.SelfReview.Review_part2`
- `coder.SelfReview.Review_part3`
- `coder.SelfReview.Review_part4`
- `coder.SelfReview.critiqueParallel_part2`
- `coder.SelfReview.hasUnfixedCritical`
- `coder.SelfReview.parseFindings_part2`
- `coder._`
- `coder.selfReviewLLM`
- `self_review.go`

### Community 138: client_test (12 nodes, cohesion=0.101)

- `git.TestAddRemote`
- `git.TestCheckout`
- `git.TestCreateBranch`
- `git.TestCurrentBranch`
- `git.TestDefaultBranch`
- `git.TestDetectOwnerRepo_HTTPS`
- `git.TestGetRemoteURL`
- `git.TestNew`
- `git.TestReadFile_NonExistent`
- `git.TestWriteAndReadFile`
- `git.initClientTestRepo`
- `client_test.go`

### Community 139: static_analysis (12 nodes, cohesion=0.132)

- `analyzer.goComplexity`
- `selfaudit.StaticAnalyzer`
- `selfaudit.StaticAnalyzer.Analyze`
- `selfaudit.StaticAnalyzer.analyzeAST`
- `selfaudit.append`
- `selfaudit.checkDeepNesting`
- `selfaudit.checkIgnoredErrors`
- `selfaudit.checkLargeFunctions`
- `selfaudit.isCallResultUsed`
- `selfaudit.measureNesting`
- `ast.Inspect`
- `static_analysis.go`

### Community 140: base_forge_test (12 nodes, cohesion=0.083)

- `handlers.NewComment`
- `handlers.TestCommentHandler_GetBase`
- `handlers.TestCommentHandler_Process_EmptyItem`
- `handlers.TestCommentHandler_ProcessedComments`
- `handlers.TestCommentProcess_InvalidNotification`
- `handlers.TestCommentProcess_NoIssueNumber`
- `handlers.TestCommentProcess_NoNewComments`
- `handlers.TestCommentProcess_ProcessedInMemory`
- `handlers.TestCommentProcess_WithOwnComment`
- `handlers.TestNewComment`
- `handlers.TestNewCommentWithMock`
- `handlers.make`

### Community 141: toolloop_e2e_test (12 nodes, cohesion=0.055)

- `e2e.TestToolLoopE2E_NoToolJustAnswer`
- `e2e.TestToolLoopE2E_OpenAIBackend_part2`
- `e2e.TestToolLoopE2E_OpenAIBackend_part3`
- `e2e.TestToolLoopE2E_OpenAIBackend_part4`
- `e2e.TestToolLoopE2E_TwoTurnFlow_part1`
- `e2e.TestToolLoopE2E_TwoTurnFlow_part2`
- `e2e.TestToolLoopE2E_TwoTurnFlow_part3`
- `e2e.applyMockConfig`
- `e2e.capturedTurn`
- `e2e.mockOllama`
- `e2e.newMockOllama`
- `toolloop_e2e_test.go`

### Community 142: subagent (12 nodes, cohesion=0.025)

- `subagent.Agent`
- `subagent.Artifact`
- `subagent.Config`
- `subagent.Pool`
- `subagent.Pool.Active`
- `subagent.Pool.Get`
- `subagent.Result`
- `subagent.State`
- `subagent.StateCreated`
- `subagent.Type`
- `subagent.TypeResearcher`
- `subagent.go`

### Community 143: benchmark_print_test (12 nodes, cohesion=0.098)

- `cmd.TestCaptureStdout_Basic`
- `cmd.TestPrintLeaderboard`
- `cmd.TestPrintLeaderboard_Empty`
- `cmd.TestPrintLeaderboard_LongModelName`
- `cmd.TestPrintLeaderboard_Single`
- `cmd.TestPrintModelReport`
- `cmd.TestPrintModelReport_ZeroPass`
- `cmd.TestPrintTaskLine_Fail`
- `cmd.TestPrintTaskLine_Pass`
- `cmd.captureStdout`
- `cmd.fn`
- `benchmark_print_test.go`

### Community 144: agent (11 nodes, cohesion=0.027)

- `cmd.agentCmd`
- `cmd.agentPath`
- `cmd.agentWorkCmd`
- `cmd.runAgentWork_part2`
- `cmd.runAgentWork_part3`
- `cmd.runAgentWork_part4`
- `cmd.runAgentWork_part5`
- `cmd.runAgentWork_part6`
- `cmd.runAgentWork_part7`
- `cmd.runAgentWork_part8`
- `agent.go`

### Community 145: install (11 nodes, cohesion=0.027)

- `cmd.bashCompletion_part1`
- `cmd.bashCompletion_part2`
- `cmd.fishCompletion_part1`
- `cmd.fishCompletion_part2`
- `cmd.installBinary_part2`
- `cmd.installCmd`
- `cmd.runUninstall_part2`
- `cmd.uninstallCmd`
- `cmd.zshCompletion_part1`
- `cmd.zshCompletion_part2`
- `install.go`

### Community 146: gleann (11 nodes, cohesion=0.027)

- `graphstore.GleannClient`
- `graphstore.GleannClient.Callees`
- `graphstore.GleannClient.Callers`
- `graphstore.GleannClient.Name`
- `graphstore.GleannClient.SymbolsInFile`
- `graphstore.communitiesResp`
- `graphstore.graphQueryReq`
- `graphstore.graphQueryResp`
- `graphstore.impactReq`
- `graphstore.impactResp`
- `gleann.go`

### Community 147: factory (11 nodes, cohesion=0.027)

- `featurefactory.DecomposeFunc`
- `featurefactory.ExecuteFunc`
- `featurefactory.Factory`
- `featurefactory.Factory.Implement_part2`
- `featurefactory.PlanFunc`
- `featurefactory.QualityFunc`
- `featurefactory.QualityReport`
- `featurefactory.Result`
- `featurefactory.Task`
- `featurefactory.TaskResult`
- `factory.go`

### Community 148: analyzer (11 nodes, cohesion=0.027)

- `analyzer.Analyzer`
- `analyzer.CallInfo`
- `analyzer.ClassInfo`
- `analyzer.FileAnalysis`
- `analyzer.FunctionInfo`
- `analyzer.ImportInfo`
- `analyzer.analyzeGo_part2`
- `analyzer.analyzeGo_part3`
- `analyzer.analyzeGo_part4`
- `analyzer.langExtensions`
- `analyzer.go`

### Community 149: intent (11 nodes, cohesion=0.027)

- `intent.Analysis`
- `intent.Detector`
- `intent.Detector.DetectSimple`
- `intent.Detector.detectWithLLM_part2`
- `intent.IntentFix`
- `intent.ReviewGeneral`
- `intent.intentKeywords_part1`
- `intent.intentKeywords_part2`
- `intent.languageKeywords`
- `intent.reviewKeywords`
- `intent.go`

### Community 150: docgen_test (11 nodes, cohesion=0.176)

- `docgen.AnalyzeAffectedModules`
- `docgen.GenerateCommitMessage`
- `docgen.TestAnalyzeAffectedModules_StagedDiff`
- `docgen.TestAnalyzeArchitecture_Go`
- `docgen.TestGenerateCommitMessage_HeuristicFallback`
- `docgen.TestGenerateCommitMessage_NoChanges`
- `docgen.TestGenerateHandover`
- `docgen.initRepo`
- `docgen.mustRun`
- `docgen.writeFile`
- `docgen_test.go`

### Community 151: helpers_test (11 nodes, cohesion=0.027)

- `e2e.MockA2AServer_part2`
- `e2e.MockForgeServer_part2`
- `e2e.MockForgeServer_part3`
- `e2e.MockGleannServer_part2`
- `e2e.TestEnvConfig`
- `e2e.TestEnvConfig.RequireAll`
- `e2e.TestEnvConfig.RequireForge`
- `e2e.TestEnvConfig.RequireGleann`
- `e2e.TestEnvConfig.RequireLLM`
- `e2e.TestEnvConfig.RequireOllama`
- `helpers_test.go`

### Community 152: depth_extended_test (11 nodes, cohesion=0.091)

- `reviewer.Critic.CritiquePlan`
- `reviewer.TestParseCriticResponse_BulletSuggestions`
- `reviewer.TestParseCriticResponse_DefaultConfidence`
- `reviewer.TestParseCriticResponse_EmptyText`
- `reviewer.TestParseCriticResponse_FallbackConfidence`
- `reviewer.TestParseCriticResponse_Invalid`
- `reviewer.TestParseCriticResponse_InvalidCode`
- `reviewer.TestParseCriticResponse_Suggestions`
- `reviewer.TestParseCriticResponse_Valid`
- `reviewer.TestParseCriticResponse_ValidCode`
- `reviewer.parseCriticResponse`

### Community 153: test (11 nodes, cohesion=0.091)

- `test.<script>`
- `test.binary`
- `test.call`
- `test.deepStrictEqual`
- `test.describe`
- `test.extraEnv`
- `test.it`
- `test.log`
- `test.quote`
- `test.require`
- `test.strictEqual`

### Community 154: warmpool_test (11 nodes, cohesion=0.097)

- `subagent.NewWarmPool`
- `subagent.TestWarmPool_AcquireEmpty`
- `subagent.TestWarmPool_AcquireOrSpawn`
- `subagent.TestWarmPool_AcquireSubmitRelease`
- `subagent.TestWarmPool_ConcurrentSubmit`
- `subagent.TestWarmPool_ExecutorError`
- `subagent.TestWarmPool_Stop_CancelsWaiting`
- `subagent.TestWarmPool_Timeout`
- `subagent.TestWarmPool_WarmUp`
- `subagent.testExecutor`
- `warmpool_test.go`

### Community 155: benchmark_llm (11 nodes, cohesion=0.027)

- `cmd.benchmarkLLMCmd_part1`
- `cmd.defaultLLMTasks_part2`
- `cmd.defaultLLMTasks_part3`
- `cmd.llmBenchReport`
- `cmd.llmModelReport`
- `cmd.llmTask`
- `cmd.llmTaskResult`
- `cmd.runBenchmarkLLM_part2`
- `cmd.runBenchmarkLLM_part3`
- `cmd.runOneModel_part2`
- `benchmark_llm.go`

### Community 156: codesearch_test (11 nodes, cohesion=0.027)

- `cmd.mockGS`
- `cmd.mockGS.Available`
- `cmd.mockGS.Callees`
- `cmd.mockGS.Callers`
- `cmd.mockGS.Communities`
- `cmd.mockGS.Impact`
- `cmd.mockGS.IndexDir`
- `cmd.mockGS.Name`
- `cmd.mockGS.Stats`
- `cmd.mockGS.SymbolsInFile`
- `codesearch_test.go`

### Community 157: manager_test (11 nodes, cohesion=0.095)

- `indexmanager.Manager.load`
- `indexmanager.TestManager_AddSharedIndex`
- `indexmanager.TestManager_IndexFor_Convention`
- `indexmanager.TestManager_IndexFor_Explicit`
- `indexmanager.TestManager_IndexesFor_AutoInclude`
- `indexmanager.TestManager_ListRepos`
- `indexmanager.TestManager_NoDuplicateAutoInclude`
- `indexmanager.TestManager_Register`
- `indexmanager.TestManager_SaveLoad`
- `indexmanager.make`
- `manager_test.go`

### Community 158: main (11 nodes, cohesion=0.027)

- `mock-llm.chatRequest`
- `mock-llm.chatResponse`
- `mock-llm.choice`
- `mock-llm.generateResponse_part2`
- `mock-llm.generateResponse_part3`
- `mock-llm.generateResponse_part4`
- `mock-llm.message`
- `mock-llm.modelItem`
- `mock-llm.modelsResponse`
- `mock-llm.usage`
- `main.go`

### Community 159: timing_test (11 nodes, cohesion=0.091)

- `timing.GetMetricsFooter`
- `timing.TestGetMetricsFooterAutoSpeedup`
- `timing.TestGetMetricsFooterBasic`
- `timing.TestGetMetricsFooterNoSpeedup`
- `timing.TestGetMetricsFooterWithBreakdown`
- `timing.TestGetMetricsFooterWithSpeedup`
- `timing.TestGetMetricsFooter_AutoCalcSpeedup`
- `timing.TestGetMetricsFooter_Basic`
- `timing.TestGetMetricsFooter_ExplicitSpeedup`
- `timing.TestGetMetricsFooter_NoSpeedupIfLessThan1`
- `timing.TestGetMetricsFooter_WithBreakdown`

### Community 160: extract_test (11 nodes, cohesion=0.104)

- `webresearch.TestExtractTextFromHTML`
- `webresearch.TestExtractTextFromHTML_Basic`
- `webresearch.TestExtractTextFromHTML_DecodesEntities`
- `webresearch.TestExtractTextFromHTML_ListItems`
- `webresearch.TestExtractTextFromHTML_RemovesComments`
- `webresearch.TestExtractTextFromHTML_RemovesFooter`
- `webresearch.TestExtractTextFromHTML_RemovesNav`
- `webresearch.TestExtractTextFromHTML_RemovesScripts`
- `webresearch.TestExtractTextFromHTML_RemovesStyles`
- `webresearch.extractTextFromHTML`
- `extract_test.go`

### Community 161: gitea_mappers_test (11 nodes, cohesion=0.104)

- `forge.TestIntVal`
- `forge.TestMapToComment`
- `forge.TestMapToIssue_FallbackAssignee`
- `forge.TestMapToIssue_Full`
- `forge.TestMapToNotification`
- `forge.TestMapToPR`
- `forge.TestMapToRepo`
- `forge.TestMapToUser_Full`
- `forge.float64`
- `forge.int`
- `gitea_mappers_test.go`

### Community 162: pipeline_test (11 nodes, cohesion=0.097)

- `retrieval.NewTokenBudget`
- `retrieval.TestContextPriority_String`
- `retrieval.TestNewTokenBudget_Default`
- `retrieval.TestTokenBudget_AddSection`
- `retrieval.TestTokenBudget_AddSection_CriticalTruncation`
- `retrieval.TestTokenBudget_BuildContext_PriorityOrder`
- `retrieval.TestTokenBudget_BuildContext_ReservedTokens`
- `retrieval.TestTokenBudget_CanFit`
- `retrieval.TestTokenBudget_Remaining`
- `retrieval.TestTokenBudget_Summary`
- `pipeline_test.go`

### Community 163: reviewer_test (10 nodes, cohesion=0.091)

- `reviewer.Agent.ReviewCode`
- `reviewer.TestParseDiff`
- `reviewer.TestParseDiffEmpty`
- `reviewer.TestParseDiffSingleFile`
- `reviewer.TestParseDiff_MalformedDiffLine`
- `reviewer.TestParseDiff_MultipleFiles`
- `reviewer.TestParseDiff_NoContent`
- `reviewer.TestReviewDecisionTypes`
- `reviewer.parseDiff`
- `reviewer_test.go`

### Community 164: patch_test (10 nodes, cohesion=0.112)

- `cmd.ApplyDiffToContent`
- `cmd.TestApplyDiffToContent_Basic`
- `cmd.TestApplyDiffToContent_ComplexWhitespace`
- `cmd.TestApplyDiffToContent_ContextMatching`
- `cmd.TestApplyDiffToContent_EmptyFile`
- `cmd.TestApplyDiffToContent_LargeFile`
- `cmd.TestApplyDiffToContent_MultiLine`
- `cmd.TestApplyDiffToContent_Whitespace`
- `cmd.parseUnifiedDiff`
- `patch_test.go`

### Community 165: index (10 nodes, cohesion=0.030)

- `cmd.indexCmd`
- `cmd.indexDetectCmd_part1`
- `cmd.indexDetectCmd_part2`
- `cmd.indexListCmd`
- `cmd.indexRegisterCmd`
- `cmd.indexResolveCmd`
- `cmd.indexSharedAddCmd`
- `cmd.indexSharedCmd`
- `cmd.indexSharedLinkCmd`
- `index.go`

### Community 166: timing (10 nodes, cohesion=0.030)

- `timing.BrandingFooter`
- `timing.ProfileSession`
- `timing.ProfileSession.AddMetadata`
- `timing.ProfileTimer`
- `timing.ProfileTimer.AddMetadata`
- `timing.StageBreakdownEntry`
- `timing.StageTiming`
- `timing.Timer`
- `timing.currentSession`
- `timing.go`

### Community 167: depth_test (10 nodes, cohesion=0.100)

- `coder.NewBranchManager`
- `coder.TestBranchManager_CurrentBranch_NonGitDir`
- `coder.TestBranchManager_DetectDefaultBranch_Fallback`
- `coder.TestBranchManager_Fetch`
- `coder.TestBranchManager_FindUniqueBranch_AllHavePR`
- `coder.TestBranchManager_FindUniqueBranch_NilFunc`
- `coder.TestBranchManager_FindUniqueBranch_NoPR`
- `coder.TestBranchManager_ForceCleanup`
- `coder.TestBranchManager_IsDirty_NonGitDir`
- `coder.TestBranchManager_New`

### Community 168: subagent_test (10 nodes, cohesion=0.134)

- `subagent.NewPool`
- `subagent.TestPool_Active`
- `subagent.TestPool_ErrorResult`
- `subagent.TestPool_Get`
- `subagent.TestPool_Results`
- `subagent.TestPool_SpawnAndWait`
- `subagent.TestPool_Timeout`
- `subagent.close`
- `subagent.mockExecutor`
- `subagent_test.go`

### Community 169: context_status_test (10 nodes, cohesion=0.127)

- `tui.NewContextStatus`
- `tui.TestContextStatus_RenderContextSection_Compressed`
- `tui.TestContextStatus_RenderContextSection_NoData`
- `tui.TestContextStatus_RenderContextSection_WithData`
- `tui.TestContextStatus_Update`
- `tui.TestContextStatus_UpdateCompressed`
- `tui.TestNewContextStatus`
- `tui.TestRenderContextBar_Half`
- `tui.stripAnsiCtx`
- `context_status_test.go`

### Community 170: classifier (10 nodes, cohesion=0.030)

- `reasoning.DefaultMaxItems`
- `reasoning.TaskClassifier`
- `reasoning.TaskType`
- `reasoning.TaskType.ComplexityHint`
- `reasoning.TaskTypeBugFix`
- `reasoning.bugKeywords`
- `reasoning.featureKeywords`
- `reasoning.refactorKeywords`
- `reasoning.testKeywords`
- `classifier.go`

### Community 171: verify_extended_test (10 nodes, cohesion=0.030)

- `cmd.TestBenchmarkRunCmdFlags`
- `cmd.TestChatCmdFlags`
- `cmd.TestConfigCmdExists`
- `cmd.TestDoctorCmdFlags`
- `cmd.TestInstallCmdExists`
- `cmd.TestMemoryCmdExists`
- `cmd.TestProjectCmdExists`
- `cmd.TestSystemCmdExists`
- `cmd.TestUninstallCmdExists`
- `verify_extended_test.go`

### Community 172: dora_selfaudit_test (10 nodes, cohesion=0.090)

- `selfaudit.New`
- `selfaudit.TestAuditor_AuditAnswer`
- `selfaudit.TestAuditor_AuditCodeChange`
- `e2e.TestE2EDORAMetricsYaverRepo_part2`
- `e2e.TestE2ESelfAuditAnswer`
- `e2e.TestE2ESelfAuditCodeChange`
- `e2e.TestE2ESelfAuditLargePatch`
- `e2e.TestE2ESelfAuditPatch`
- `auditor_test.go`
- `dora_selfaudit_test.go`

### Community 173: skills_test (10 nodes, cohesion=0.138)

- `skills.NewRegistry`
- `skills.TestRegistry_DiscoverAndLoad`
- `skills.TestRegistry_DiscoverMissingDir`
- `skills.TestRegistry_DiscoverNoFrontmatter`
- `skills.TestRegistry_Get`
- `skills.TestRegistry_LoadNotFound`
- `skills.TestRegistry_Match`
- `skills.TestRegistry_Register`
- `skills.writeSkillFile`
- `skills_test.go`

### Community 174: chunked_test (10 nodes, cohesion=0.153)

- `reviewer.TestSplitIntoChunks_ContainsLineNumbers`
- `reviewer.TestSplitIntoChunks_ExactSize`
- `reviewer.TestSplitIntoChunks_LargeFile`
- `reviewer.TestSplitIntoChunks_SingleLine`
- `reviewer.TestSplitIntoChunks_SmallFile`
- `reviewer.TestSplitIntoChunks_SmallRemainder`
- `reviewer.TestSplitIntoChunks_VeryLargeFile`
- `reviewer.makeLines`
- `reviewer.splitIntoChunks`
- `chunked_test.go`

### Community 175: client_extended_test (10 nodes, cohesion=0.116)

- `v5.PlainInit`
- `git.TestCheckout_NonExistent`
- `git.TestCheckout_Success`
- `git.TestDefaultBranch_Fallback`
- `git.TestDetectOwnerRepo_SSH`
- `git.TestFetch_NoRemote`
- `git.TestForcePush_NoRemote`
- `git.TestPush_NoRemote`
- `git.TestWriteFile_NestedDir`
- `client_extended_test.go`

### Community 176: agent_forge_test (10 nodes, cohesion=0.100)

- `social.TestMentionProcess_InvalidDataAllPaths`
- `social.TestMentionProcess_NotificationPath_GetCommentsFails`
- `social.TestMentionProcess_ZeroIssueNumber`
- `handlers.NewMention`
- `handlers.TestFIFORemaining_Initial`
- `handlers.TestMentionHandler_GetBase`
- `handlers.TestMentionHandler_Process_EmptyItem`
- `handlers.TestMentionProcess_MissingIssueData`
- `handlers.TestNewMention`
- `handlers.TestNewMentionWithMock`

### Community 177: mock_provider (10 nodes, cohesion=0.030)

- `llm.MockProvider`
- `llm.MockProvider.CallCount`
- `llm.MockProvider.ChatWithFormat`
- `llm.MockProvider.ChatWithRetry`
- `llm.MockProvider.GenerateWithTools`
- `llm.MockProvider.InvokeWithRetry`
- `llm.MockProvider.incCount`
- `llm.MockProvider.makeResponse`
- `llm.MockProvider.nextIndex`
- `mock_provider.go`

### Community 178: permissions_extended_test (10 nodes, cohesion=0.100)

- `chat.LoopAdapter.makeCheckPermission`
- `permissions.ClassifyTool`
- `permissions.TestClassifyTool`
- `permissions.TestClassifyTool_CaseInsensitive`
- `permissions.TestClassifyTool_Dangerous`
- `permissions.TestClassifyTool_Network`
- `permissions.TestClassifyTool_ReadOnly`
- `permissions.TestClassifyTool_Shell`
- `permissions.TestClassifyTool_Unknown_DefaultsToWrite`
- `permissions.TestClassifyTool_Write`

### Community 179: pipeline (10 nodes, cohesion=0.030)

- `parallelreview.FileReview`
- `parallelreview.MergedReview`
- `parallelreview.Pipeline`
- `parallelreview.Pipeline.Progress`
- `parallelreview.Pipeline.Review_part2`
- `parallelreview.Pipeline.Review_part3`
- `parallelreview.PipelineConfig`
- `parallelreview.ReviewTask`
- `parallelreview.ReviewerFunc`
- `pipeline.go`

### Community 180: onboarding (10 nodes, cohesion=0.030)

- `config.OllamaModel`
- `config.SetupWizard`
- `config.SetupWizard.DisplaySummary_part2`
- `config.SetupWizard.DisplaySummary_part3`
- `config.SetupWizard.SetupForge_part2`
- `config.SetupWizard.SetupGraphStore_part2`
- `config.SetupWizard.SetupRAG_part2`
- `config.SetupWizard.SetupVectorStore_part2`
- `config.SetupWizard.inputWithConfigDefault`
- `onboarding.go`

### Community 181: chat_test (10 nodes, cohesion=0.112)

- `chat.New`
- `chat.TestAgent_New_RealConstructor`
- `chat.TestBuildContextualPrompt`
- `chat.TestClear`
- `chat.TestHistory`
- `chat.TestMessageCount`
- `chat.TestNewAgent`
- `chat.TestSystemPrompt`
- `chat.containsStr`
- `chat_test.go`

### Community 182: handlers_test (10 nodes, cohesion=0.082)

- `handlers.TestCheckFileVolatility_WithMockGitLog`
- `handlers.TestContainsIgnoreCase`
- `handlers.TestExtractPhaseContext_HighPriority`
- `handlers.TestExtractPhaseContext_ItemOutOfRange`
- `handlers.TestFormatFooter`
- `handlers.TestHandleResult_Struct`
- `handlers.containsIgnoreCase`
- `handlers.mockReactionChecker`
- `handlers.mockReactionChecker.GetCommentReactions`
- `handlers_test.go`

### Community 183: collector_test (10 nodes, cohesion=0.150)

- `assert.NoError`
- `dora.New`
- `dora.TestCollector_Collect`
- `dora.TestCollector_CollectToFile`
- `dora.TestCollector_FindReverts`
- `dora.TestCollector_Summary`
- `dora.cleanup`
- `dora.setupTestRepo`
- `dora.setupTestRepo_part2`
- `collector_test.go`

### Community 184: gleann_backend (10 nodes, cohesion=0.100)

- `retrieval.GleannBackend.AskQueryFunc`
- `retrieval.GleannBackend.GraphQueryFunc`
- `retrieval.GleannBackend.HybridQueryFunc`
- `retrieval.GleannBackend.MultiIndexQueryFunc`
- `retrieval.GleannBackend.VectorQueryFunc`
- `retrieval.GleannBackend.formatGraphContext`
- `retrieval.GleannBackend.formatResults`
- `retrieval.MemoryBlocksAdapter.Query`
- `retrieval.MemoryEngineAdapter.Query`
- `retrieval.len`

### Community 185: report_test (10 nodes, cohesion=0.116)

- `reviewer.NewReportGenerator`
- `reviewer.TestCloseReport_Approved`
- `reviewer.TestCloseReport_ChangesRequested`
- `reviewer.TestCreateConsolidatedReport_NoMissingTests`
- `reviewer.TestCreateConsolidatedReport_WithMissingTests`
- `reviewer.TestFormatFileReview_SyntaxError`
- `reviewer.TestFormatFileReview_SyntaxOK`
- `reviewer.TestFormatFileReview_WithFindings`
- `reviewer.TestFormatFileReview_WithImpact`
- `report_test.go`

### Community 186: context (9 nodes, cohesion=0.033)

- `coder.ContextBuilder`
- `coder.ContextBuilder.Build_part2`
- `coder.ContextBuilder.Build_part3`
- `coder.ContextBuilder.Build_part4`
- `coder.ContextBuilder.scanRepoFiles_part2`
- `coder.ContextBuilder.scanRepoFiles_part3`
- `coder.maxContextFiles`
- `coder.maxFileSize`
- `context.go`

### Community 187: conversation_test (9 nodes, cohesion=0.126)

- `memory.NewSession`
- `memory.TestAddAssistantMessage`
- `memory.TestAddHumanMessage`
- `memory.TestGetContext_Empty`
- `memory.TestNewSession`
- `memory.TestSession_Close`
- `memory.TestSession_FullName_Basic`
- `memory.TestSession_MarshalJSON_Basic`
- `conversation_test.go`

### Community 188: handlers (9 nodes, cohesion=0.111)

- `fmt.Sprint`
- `clarifier.Clarifier.buildPrompt`
- `coder.TaskDecomposer.buildDecomposePrompt`
- `handlers.MentionHandler.handlePlan`
- `handlers.MentionHandler.handleQuestion`
- `prompt.Registry.Render`
- `prompt.Render`
- `prompt.TestRenderNoVars`
- `prompt.TestRenderSimple`

### Community 189: classifier_test (9 nodes, cohesion=0.033)

- `reasoning.TestClassify_BugFix`
- `reasoning.TestClassify_CaseInsensitive`
- `reasoning.TestClassify_Feature`
- `reasoning.TestClassify_Refactor`
- `reasoning.TestClassify_Test`
- `reasoning.TestClassify_Turkish`
- `reasoning.TestClassify_Unknown`
- `reasoning.TestComplexityHint`
- `classifier_test.go`

### Community 190: cmd (9 nodes, cohesion=0.181)

- `cmd.TestExpandPath`
- `cmd.TestExpandPath_Tilde`
- `cmd.TestKnownInstallDirs`
- `cmd.expandPath`
- `cmd.knownInstallDirs`
- `cmd.removeCompletions`
- `cmd.runUninstall_part1`
- `session.init`
- `os.UserHomeDir`

### Community 191: tracker (9 nodes, cohesion=0.033)

- `social.PhaseMetrics`
- `social.ProcessedItem`
- `social.StateTracker`
- `social.StateTracker.CompletePhase`
- `social.StateTracker.GetState`
- `social.StateTracker.RecordItemsDiscovered`
- `social.StateTracker.Reset`
- `social.trackerInstance`
- `tracker.go`

### Community 192: intent_extended_test (9 nodes, cohesion=0.111)

- `intent.Detector.detectWithKeywords`
- `intent.TestDetectReviewSubject`
- `intent.TestDetectReviewSubject_Architecture`
- `intent.TestDetectReviewSubject_General`
- `intent.TestDetectReviewSubject_Performance`
- `intent.TestDetectReviewSubject_Quality`
- `intent.TestDetectReviewSubject_RepoInfo`
- `intent.TestDetectReviewSubject_Security`
- `intent.detectReviewSubject`

### Community 193: analyzer_test (9 nodes, cohesion=0.164)

- `analyzer.New`
- `analyzer.TestAnalyzeGoFile`
- `analyzer.TestAnalyzeJavaScriptFile`
- `analyzer.TestAnalyzePythonFile`
- `analyzer.TestGoComplexity`
- `analyzer.TestRustFile`
- `analyzer.TestVisualizer_part2`
- `analyzer.len`
- `analyzer_test.go`

### Community 194: speculative_test (9 nodes, cohesion=0.113)

- `agentloop.NewSpeculativeExecutor`
- `agentloop.TestSpeculativeExecutor_Collect_FallsBackToSync`
- `agentloop.TestSpeculativeExecutor_Collect_UsesCache`
- `agentloop.TestSpeculativeExecutor_NoDuplicateExecution`
- `agentloop.TestSpeculativeExecutor_OnChunk_SafeToolsStartEarly`
- `agentloop.TestSpeculativeExecutor_OnChunk_UnsafeToolsSkipped`
- `agentloop.TestSpeculativeExecutor_Reset`
- `agentloop.mockToolExec`
- `speculative_test.go`

### Community 195: tracker (9 nodes, cohesion=0.111)

- `social.StateTracker.CompletePhaseWithError`
- `social.StateTracker.RecordBottleneck`
- `social.StateTracker.RecordError`
- `social.StateTracker.RecordItemProcessed`
- `social.StateTracker.completeCurrentPhase`
- `social.TestAgent_Discover_AssignedIssue`
- `social.TestAgent_Discover_Notification`
- `social.append`
- `social.mockForgeAgent.MarkNotificationRead`

### Community 196: depth_extended_test (9 nodes, cohesion=0.111)

- `reviewer.CountBySeverity`
- `reviewer.Critic.CritiqueCode_part1`
- `reviewer.TestCountBySeverity`
- `reviewer.TestCountBySeverityEmptyCoverage`
- `reviewer.TestCountBySeverityMixedCoverage`
- `reviewer.TestCountBySeverity_AllLevels`
- `reviewer.TestCountBySeverity_EmptyList`
- `reviewer.TestCountBySeverity_MixedSeverities`
- `reviewer.TestCountBySeverity_NoMatchingSeverity`

### Community 197: intent_extended_test (9 nodes, cohesion=0.111)

- `intent.TestDetectLanguage`
- `intent.TestDetectLanguage_Go`
- `intent.TestDetectLanguage_Java`
- `intent.TestDetectLanguage_JavaScript`
- `intent.TestDetectLanguage_NoMatch`
- `intent.TestDetectLanguage_Python`
- `intent.TestDetectLanguage_Rust`
- `intent.TestDetectLanguage_TypeScript`
- `intent.detectLanguage`

### Community 198: sandbox_test (9 nodes, cohesion=0.099)

- `sandbox.New`
- `sandbox.TestExecuteBash`
- `sandbox.TestExecuteBashFailure`
- `sandbox.TestExecuteBashWithArgs`
- `sandbox.TestExecuteDefaultTimeout`
- `sandbox.TestExecuteEmptyCode`
- `sandbox.TestNew`
- `sandbox.TestResultFields`
- `sandbox_test.go`

### Community 199: onboarding (9 nodes, cohesion=0.111)

- `config.SetupWizard.SetupCompletion`
- `config.SetupWizard.SetupForge_part1`
- `config.SetupWizard.SetupGraphStore_part1`
- `config.SetupWizard.SetupOptional`
- `config.SetupWizard.SetupRAG_part1`
- `config.SetupWizard.SetupVectorStore_part1`
- `config.TestLoadExistingEnv_Missing`
- `config.make`
- `config.removeYaverCompletions`

### Community 200: verify (9 nodes, cohesion=0.033)

- `cmd.runVerifySocialLive_part2`
- `cmd.verifyAllCmd`
- `cmd.verifyCmd`
- `cmd.verifyRepoPath`
- `cmd.verifySocialAssignmentCmd`
- `cmd.verifySocialLiveCmd`
- `cmd.verifySocialReviewCmd`
- `cmd.verifyUnitCmd`
- `verify.go`

### Community 201: zombie (9 nodes, cohesion=0.167)

- `handlers.Base.CheckZombieCommentReaction`
- `handlers.Base.CheckZombieReactionEnhanced_part1`
- `handlers.TestRetryCounters`
- `handlers.TestSaveAndLoad_Roundtrip`
- `handlers.delete`
- `handlers.getRetryCount`
- `handlers.incrementRetryCount`
- `handlers.loadRetryCounts`
- `handlers.resetRetryCount`

### Community 202: gleann_check (9 nodes, cohesion=0.085)

- `tui.RunGleannInstallCheck_part1`
- `tui.RunGleannInstallCheck_part2`
- `tui.RunGleannInstallCheck_part3`
- `tui.detachProcess`
- `tui.probeGleannServer`
- `tui.startGleannBackground`
- `gleann_check.go`
- `gleann_check_other.go`
- `gleann_check_unix.go`

### Community 203: helpers_test (9 nodes, cohesion=0.111)

- `handlers.TestFindUnprocessedComments_Basic`
- `handlers.TestFindUnprocessedComments_FIFOOrder`
- `handlers.TestFindUnprocessedComments_SkipCancelled`
- `handlers.TestFindUnprocessedComments_SkipOwnComments`
- `handlers.TestFindUnprocessedComments_SkipProcessed`
- `handlers.TestFindUnprocessedComments_SkipsAgentComments`
- `handlers.TestFindUnprocessedComments_UserCancelled`
- `handlers.findUnprocessedComments`
- `regexp.QuoteMeta`

### Community 204: social (9 nodes, cohesion=0.033)

- `cmd.socialCmd`
- `cmd.socialDigestCmd_part1`
- `cmd.socialDigestCmd_part2`
- `cmd.socialMetricsCmd_part1`
- `cmd.socialMetricsCmd_part2`
- `cmd.socialRunCmd_part1`
- `cmd.socialRunCmd_part2`
- `cmd.socialRunCmd_part3`
- `social.go`

### Community 205: self_review_test (9 nodes, cohesion=0.033)

- `coder.TestSelfReview_ApplyFixes`
- `coder.TestSelfReview_ApplyFixes_NoMatch`
- `coder.TestSelfReview_CalculateScore`
- `coder.TestSelfReview_HasUnfixedCritical`
- `coder.mockSelfReviewLLM`
- `coder.mockSelfReviewLLM.Generate`
- `coder.mockSelfReviewLLM.GenerateWithTools`
- `coder.mockSelfReviewLLM.IsOpenAIClient`
- `self_review_test.go`

### Community 206: handover (9 nodes, cohesion=0.140)

- `docgen.AnalyzeArchitecture`
- `docgen.GenerateHandover`
- `docgen.HandoverOptions`
- `docgen.exists`
- `docgen.writeHandoverConfig`
- `docgen.writeHandoverHeader`
- `docgen.writeHandoverSetup`
- `docgen.writeHandoverTesting`
- `handover.go`

### Community 207: collector (9 nodes, cohesion=0.033)

- `metrics.Collector`
- `metrics.Collector.RecordSelfAuditWithDORA`
- `metrics.Collector.linkSelfAuditToDORA`
- `metrics.DORAState`
- `metrics.HandlerStats`
- `metrics.Metrics`
- `metrics.SelfAuditState`
- `metrics.collectorInstance`
- `collector.go`

### Community 208: patch_applier (9 nodes, cohesion=0.033)

- `cmd.diffContext`
- `cmd.diffHunk`
- `cmd.diffLine`
- `cmd.diffLineType`
- `cmd.findHunkLocation_part2`
- `cmd.parseUnifiedDiff_part2`
- `cmd.unifiedDiff`
- `cmd.unifiedDiff.applyToContent_part2`
- `patch_applier.go`

### Community 209: webresearch_test (8 nodes, cohesion=0.125)

- `coder.WebSearchExecutor.Execute`
- `reasoning.ReasoningLoop.ExecuteTool`
- `webresearch.SummarizeSearchResponse`
- `webresearch.TestSummarizeSearchResponse`
- `webresearch.TestSummarizeSearchResponse_Empty`
- `webresearch.TestSummarizeSearchResponse_Error`
- `webresearch.TestSummarizeSearchResponse_Nil`
- `webresearch.TestSummarizeSearchResponse_WithResults`

### Community 210: sdd_test (8 nodes, cohesion=0.161)

- `cmd.TestLoadOrCreateState_New`
- `cmd.TestLoadOrCreateState_NoFile`
- `cmd.TestLoadOrCreateState_NoResume`
- `cmd.TestLoadOrCreateState_ResumeWrongRequirement`
- `cmd.TestSaveAndLoadState`
- `cmd.TestSaveState_CreatesDir`
- `cmd.loadOrCreateState`
- `cmd.saveState`

### Community 211: autocomplete (8 nodes, cohesion=0.037)

- `tui.autocompleteState`
- `tui.autocompleteState.accept`
- `tui.autocompleteState.dismiss`
- `tui.autocompleteState.moveUp`
- `tui.defaultSlashCommands_part1`
- `tui.defaultSlashCommands_part2`
- `tui.slashCommand`
- `autocomplete.go`

### Community 212: skills (8 nodes, cohesion=0.037)

- `skills.Meta`
- `skills.Registry`
- `skills.Registry.Get`
- `skills.Registry.Register`
- `skills.Skill`
- `skills.Source`
- `skills.SourceFile`
- `skills.go`

### Community 213: coder_coverage_test (8 nodes, cohesion=0.123)

- `coder.BuildRunner.Run`
- `coder.TestContextBuilderBuildEmptyCoverage`
- `coder.TestRunCommandEchoCoverage`
- `coder.TestRunCommandNotFoundCoverage`
- `coder.TestRunner.Run`
- `coder.lookPath`
- `coder.runCommand`
- `coder_coverage_test.go`

### Community 214: prompt (8 nodes, cohesion=0.037)

- `prompt.Registry`
- `prompt.Registry.Has`
- `prompt.Registry.MustGet`
- `prompt.Registry.Register`
- `prompt.Template`
- `prompt.defaultRegistry`
- `prompt.templateFS`
- `prompt.go`

### Community 215: client (8 nodes, cohesion=0.037)

- `git.Client`
- `git.Client.AddAll`
- `git.Client.AddRemote`
- `git.Client.CurrentBranch`
- `git.Client.Fetch`
- `git.Client.ForcePush`
- `git.Client.Push`
- `client.go`

### Community 216: coder_test (8 nodes, cohesion=0.116)

- `coder.NewFileWriter`
- `coder.TestFileWriterLanguageColonWithPath`
- `coder.TestFileWriterMixedFormats`
- `coder.TestFileWriterNoRepoPath`
- `coder.TestFileWriterSkipsPlainLanguageBlocks`
- `coder.TestValidateNotDestructive`
- `coder_test.go`
- `writer_test.go`

### Community 217: handlers (8 nodes, cohesion=0.196)

- `handlers.AssignmentHandler.Process_part1`
- `handlers.CommentHandler.Process_part1`
- `handlers.MentionHandler.Process_part1`
- `handlers.ReviewHandler.Process_part1`
- `handlers.TestExtractNumberFromURL`
- `handlers.cancel`
- `handlers.extractNumberFromURL`
- `handlers.newSessionFromRepo`

### Community 218: install_test (8 nodes, cohesion=0.129)

- `cmd.TestBashCompletion`
- `cmd.TestFishCompletion`
- `cmd.TestZshCompletion`
- `cmd.bashCompletion`
- `cmd.containsAll`
- `cmd.fishCompletion`
- `cmd.zshCompletion`
- `install_test.go`

### Community 219: cache_test (8 nodes, cohesion=0.139)

- `prompt.NewSectionCache`
- `prompt.TestSectionCache_Clear`
- `prompt.TestSectionCache_GetPut`
- `prompt.TestSectionCache_Invalidate`
- `prompt.TestSectionCache_InvalidateOnInputChange`
- `prompt.TestSectionCache_RenderCached`
- `prompt.TestSectionCache_RenderCached_MissingTemplate`
- `cache_test.go`

### Community 220: extension.test (8 nodes, cohesion=0.037)

- `extension.test.ts`
- `test.Module`
- `test.env`
- `test.ext`
- `test.gap_3`
- `test.gap_4`
- `test.originalResolve`
- `test.vscodeStub`

### Community 221: litellm_live_e2e_test (8 nodes, cohesion=0.152)

- `e2e.DetectEnv`
- `e2e.TestGleann_LiveGraphQuery`
- `e2e.TestLiteLLM_LiveSmoke_part1`
- `e2e.TestLiteLLM_LiveSmoke_part2`
- `e2e.TestLiteLLM_ModelsEndpoint`
- `e2e.fetchURL`
- `e2e.truncateForTest`
- `litellm_live_e2e_test.go`

### Community 222: skill_router_test (8 nodes, cohesion=0.105)

- `handlers.NewSkillRouter`
- `handlers.TestSkillRouterNilRegistry`
- `handlers.TestSkillRouter_CanRoute_NoRegistry`
- `handlers.TestSkillRouter_Route_NoRegistry`
- `handlers.TestSkillRouter_Route_WithA2AAgent_part2`
- `handlers.TestTryA2ARouting_SkillRouterNilRegistry`
- `e2e.TestAgentRegistryE2E_UnavailableAgent`
- `skill_router_test.go`

### Community 223: validator (8 nodes, cohesion=0.037)

- `configvalidator.Config`
- `configvalidator.Neo4jConfig`
- `configvalidator.OllamaConfig`
- `configvalidator.ProjectConfig`
- `configvalidator.Result`
- `configvalidator.Validator`
- `configvalidator.VectorDBConfig`
- `validator.go`

### Community 224: types (8 nodes, cohesion=0.037)

- `memory.BackendSQLite`
- `memory.BackendType`
- `memory.ContextResult`
- `memory.Entry`
- `memory.MemoryEpisodic`
- `memory.MemoryType`
- `memory.SearchResult`
- `types.go`

### Community 225: pipeline_test (8 nodes, cohesion=0.161)

- `retrieval.GleannBackend.ProgressiveQueryFunc`
- `retrieval.Pipeline.GetContext_part1`
- `retrieval.TestBuildSessionContext`
- `retrieval.TestBuildSessionContext_Empty`
- `retrieval.TestBuildSessionContext_Truncates`
- `retrieval.append`
- `retrieval.buildSessionContext`
- `retrieval.opt`

### Community 226: chat_extended_test (8 nodes, cohesion=0.104)

- `chat.TestChatMessage_Fields`
- `chat.TestChatRoles`
- `chat.TestFirstLine_Empty`
- `chat.TestFirstLine_Multi`
- `chat.TestFirstLine_OnlyNewline`
- `chat.TestFirstLine_Single`
- `chat.firstLine`
- `chat_extended_test.go`

### Community 227: loop_adapter_test (8 nodes, cohesion=0.104)

- `chat.TestExtractA2ATaskContent_EmptyArtifactSkipped`
- `chat.TestExtractA2ATaskContent_FallbackToStatus`
- `chat.TestExtractA2ATaskContent_FromArtifacts`
- `chat.TestExtractA2ATaskContent_FromHistory`
- `chat.TestMakeCheckPermission_NilPermissions`
- `chat.TestMakeFireHook_NilHooks`
- `chat.extractA2ATaskContent`
- `loop_adapter_test.go`

### Community 228: clarifier_test (8 nodes, cohesion=0.104)

- `clarifier.TestParseResponse_Ambiguous`
- `clarifier.TestParseResponse_Clear`
- `clarifier.TestParseResponse_TooBroad`
- `clarifier.TestPathEffortParsing`
- `clarifier.TestSplitList`
- `clarifier.TestSplitList_Empty`
- `clarifier.len`
- `clarifier_test.go`

### Community 229: codeparse (8 nodes, cohesion=0.037)

- `codeparse.Analyze_part2`
- `codeparse.ClassInfo`
- `codeparse.ExtractedAnalysis`
- `codeparse.FunctionInfo`
- `codeparse.ImportInfo`
- `codeparse.Symbol`
- `codeparse.defaultParser`
- `codeparse.go`

### Community 230: router (8 nodes, cohesion=0.037)

- `tools.MCPCaller`
- `tools.RouterOption`
- `tools.SkillCaller`
- `tools.SkillInfo`
- `tools.UnifiedRouter`
- `tools.UnifiedRouter.Execute_part2`
- `tools.UnifiedRouter.RegisterA2ATool`
- `router.go`

### Community 231: configset_onboard_test (8 nodes, cohesion=0.139)

- `tui.NewConfigSet`
- `tui.TestConfigSetView_Done_Cancelled`
- `tui.TestConfigSetView_Done_Saved`
- `tui.TestConfigSetView_FetchError`
- `tui.TestConfigSetView_Fetching`
- `tui.TestConfigSetView_WithModels`
- `tui.TestNewConfigSet`
- `configset_onboard_test.go`

### Community 232: chat_e2e_test (8 nodes, cohesion=0.037)

- `e2e.TestChatE2E_AGENTSmd_part2`
- `e2e.TestChatE2E_Headless_part2`
- `e2e.TestChatE2E_Headless_part3`
- `e2e.TestChatE2E_SlashCommands_part2`
- `e2e.TestChatE2E_SlashCommands_part3`
- `e2e.TestChatE2E_SlashCommands_part4`
- `e2e.TestChatE2E_SlashCommands_part5`
- `chat_e2e_test.go`

### Community 233: context_info_test (8 nodes, cohesion=0.152)

- `agentloop.ComputeContextInfo`
- `agentloop.TestComputeContextInfo_Compressed`
- `agentloop.TestComputeContextInfo_Empty`
- `agentloop.TestComputeContextInfo_HalfUsed`
- `agentloop.TestComputeContextInfo_OverLimit`
- `agentloop.TestComputeContextInfo_ZeroMax`
- `agentloop.int`
- `context_info_test.go`

### Community 234: notification (8 nodes, cohesion=0.037)

- `tui.Notification`
- `tui.NotificationType`
- `tui.NotifyInfo`
- `tui.defaultNotifyTTL`
- `tui.notificationIndicators`
- `tui.notifyExpireMsg`
- `tui.shortNotifyTTL`
- `notification.go`

### Community 235: benchmark_llm_test (8 nodes, cohesion=0.129)

- `cmd.TestBenchmarkLLM_JSONVerifier_StripsFence`
- `cmd.TestBenchmarkLLM_RefusalVerifier`
- `cmd.TestBenchmarkLLM_TaskCount`
- `cmd.TestBenchmarkLLM_TaskVerifiers`
- `cmd.defaultLLMTasks`
- `cmd.v`
- `cmd.verify`
- `benchmark_llm_test.go`

### Community 236: coverage_test (8 nodes, cohesion=0.123)

- `handlers.MentionHandler.handlePRCodingMention_part1`
- `handlers.TestEditCommentNoPanic`
- `handlers.TestMakeProgressCallbackReturnsNonNil`
- `handlers.TestMakeProgressCallbackWithData`
- `handlers.TestMakeProgressCallback_NonNil`
- `handlers.cb`
- `handlers.makeProgressCallback`
- `coverage_test.go`

### Community 237: configset (8 nodes, cohesion=0.037)

- `tui.ConfigSetModel`
- `tui.ConfigSetModel.View_part2`
- `tui.ConfigSetModel.View_part3`
- `tui.ConfigSetModel.View_part4`
- `tui.ConfigSetModel.saveConfig_part2`
- `tui.configRoleField`
- `tui.configRoles`
- `configset.go`

### Community 238: mode (8 nodes, cohesion=0.125)

- `mode.Actor.Execute`
- `mode.Actor.buildStepPrompt`
- `mode.Actor.dependenciesMet`
- `mode.ExecutionPlan.TotalSteps`
- `mode.Planner.fallbackPlan`
- `mode.TestPlanner_Plan_InvalidJSON_Fallback`
- `mode.TestPlanner_Plan_NilClient_Fallback`
- `mode.len`

### Community 239: depth_test (8 nodes, cohesion=0.125)

- `reviewer.AnalyzeCrossFileImpacts`
- `reviewer.TestAnalyzeCrossFileImpacts_CoreComponent`
- `reviewer.TestAnalyzeCrossFileImpacts_CoreFiles`
- `reviewer.TestAnalyzeCrossFileImpacts_MultipleFilesInDir`
- `reviewer.TestAnalyzeCrossFileImpacts_MultipleInSameDir`
- `reviewer.TestAnalyzeCrossFileImpacts_NoWarnings`
- `reviewer.TestAnalyzeCrossFileImpacts_None`
- `reviewer.TestGenerateReport_WithCrossFileDeps`

### Community 240: webhook_bridge_test (8 nodes, cohesion=0.121)

- `social.NewWebhookBridge`
- `social.TestWebhookBridge_ConvertIssueAssigned`
- `social.TestWebhookBridge_ConvertIssueComment`
- `social.TestWebhookBridge_ConvertPROpened`
- `social.TestWebhookBridge_ConvertUnknown`
- `social.TestWebhookBridge_EventChannel`
- `social._`
- `webhook_bridge_test.go`

### Community 241: context_test (8 nodes, cohesion=0.139)

- `reviewer.NewContextBuilder`
- `reviewer.TestContextBuilder_LongSnippetTruncated`
- `reviewer.TestContextBuilder_NilPipeline`
- `reviewer.TestContextBuilder_ScannerFindings`
- `reviewer.TestContextBuilder_WithPipeline`
- `reviewer.TestGetImpactAnalysis_EmptyContext`
- `reviewer.TestGetImpactAnalysis_NilPipeline`
- `context_test.go`

### Community 242: runners (8 nodes, cohesion=0.037)

- `coder.BuildResult`
- `coder.BuildRunner`
- `coder.BuildRunner.Detect_part2`
- `coder.BuildRunner.RunAndVerify`
- `coder.BuildSystem`
- `coder.TestResult`
- `coder.TestRunner`
- `runners.go`

### Community 243: scanner_test (8 nodes, cohesion=0.139)

- `reviewer.NewScannerIntegrator`
- `reviewer.TestCheckSyntax_AlwaysValid`
- `reviewer.TestRunScanners_EmptyCode`
- `reviewer.TestRunScanners_GoIssues`
- `reviewer.TestRunScanners_HighComplexity`
- `reviewer.TestRunScanners_PythonSecurity`
- `reviewer.TestRunScanners_UnsupportedExt`
- `scanner_test.go`

### Community 244: static_analysis_test (8 nodes, cohesion=0.139)

- `selfaudit.TestAnalyzeAST_IgnoredErrors`
- `selfaudit.TestAnalyzeAST_NoIssues`
- `selfaudit.TestParseVetOutput_CriticalIssue`
- `selfaudit.TestParseVetOutput_Empty`
- `selfaudit.TestParseVetOutput_MultipleLines`
- `selfaudit.TestParseVetOutput_WarningIssue`
- `selfaudit.len`
- `static_analysis_test.go`

### Community 245: speculative_test (8 nodes, cohesion=0.179)

- `agentloop.SpeculativeExecutor.CachedCount`
- `agentloop.SpeculativeExecutor.PendingCount`
- `agentloop.TestDetectToolCalls_Incomplete`
- `agentloop.TestDetectToolCalls_Multiple`
- `agentloop.TestDetectToolCalls_NoJSON`
- `agentloop.TestDetectToolCalls_Single`
- `agentloop.detectToolCalls`
- `agentloop.len`

### Community 246: memory (8 nodes, cohesion=0.037)

- `cmd.memoryCleanCmd_part1`
- `cmd.memoryCleanCmd_part2`
- `cmd.memoryCmd`
- `cmd.memoryDeleteCmd`
- `cmd.memoryListCmd`
- `cmd.memoryNewCmd`
- `cmd.memorySwitchCmd`
- `memory.go`

### Community 247: debug (7 nodes, cohesion=0.043)

- `debug.Init_part2`
- `debug.Options`
- `debug.fileHandler`
- `debug.fileHandler.Enabled`
- `debug.fileHandler.WithGroup`
- `debug.mu`
- `debug.go`

### Community 248: progress (7 nodes, cohesion=0.043)

- `handlers.PlanStep`
- `handlers.PostPlanOpts`
- `handlers.ProgressReporter`
- `handlers.ProgressReporter.Disable`
- `handlers.ProgressReporter.UpdateStep`
- `handlers.StatusIcons`
- `progress.go`

### Community 249: agent_forge_test (7 nodes, cohesion=0.143)

- `errors.New`
- `social.TestAgent_IsCommentProcessed_ForgeError`
- `social.TestMentionProcess_IssueDirectPath`
- `social.TestMentionProcess_SubjectPath`
- `featurefactory.errExecute`
- `featurefactory.failDecompose`
- `featurefactory.failPlan`

### Community 250: tui_helpers_test (7 nodes, cohesion=0.143)

- `tui.TestIsApproval_English`
- `tui.TestIsApproval_False`
- `tui.TestIsApproval_NotApproval`
- `tui.TestIsApproval_True`
- `tui.TestIsApproval_Turkish`
- `tui.TestIsApproval_WithWhitespace`
- `tui.isApproval`

### Community 251: theme (7 nodes, cohesion=0.043)

- `ui.Bordered`
- `ui.BrandTitle`
- `ui.DefaultTheme`
- `ui.StatusBadge`
- `ui.T`
- `ui.Theme`
- `theme.go`

### Community 252: base_forge_test (7 nodes, cohesion=0.143)

- `handlers.NewAssignment`
- `handlers.TestAssignmentHandler_GetBase`
- `handlers.TestAssignmentHandler_Process_EmptyItem`
- `handlers.TestAssignmentProcess_MissingIssueData`
- `handlers.TestAssignmentProcess_ZombieSkipped`
- `handlers.TestNewAssignment`
- `handlers.TestNewAssignmentWithMock`

### Community 253: agent_di_test (7 nodes, cohesion=0.155)

- `social.NewAgent`
- `social.TestDiscoverWithNilForge`
- `social.TestNewAgentSetsAllHandlers`
- `social.TestNewAgentWithMockProvider`
- `social.TestNewAgentWithNilProvider`
- `social.TestRunWithCancelledContext`
- `agent_di_test.go`

### Community 254: base_forge_test (7 nodes, cohesion=0.143)

- `handlers.NewReview`
- `handlers.TestNewReview`
- `handlers.TestNewReviewWithMock`
- `handlers.TestReviewHandler_GetBase`
- `handlers.TestReviewHandler_Process_EmptyItem`
- `handlers.TestReviewProcess_InvalidNotification`
- `handlers.TestReviewProcess_NoPRNumber`

### Community 255: cache_test (7 nodes, cohesion=0.143)

- `sha256.New`
- `prompt.SectionCache.Get`
- `prompt.SectionCache.Put`
- `prompt.TestHashVars_Deterministic`
- `prompt.TestHashVars_Different`
- `prompt.TestHashVars_Empty`
- `prompt.hashVars`

### Community 256: github (7 nodes, cohesion=0.238)

- `forge.GitHub.GetPRDiff`
- `forge.GitHub.do`
- `forge.GitLab.GetPRDiff`
- `forge.GitLab.do`
- `forge.Gitea.do`
- `forge.string`
- `http.NewRequest`

### Community 257: apiserver (7 nodes, cohesion=0.143)

- `apiserver.WithA2A`
- `apiserver.WithWebhook`
- `cmd.cancelHealth`
- `cmd.executeCodeTaskAtomic`
- `cmd.getCodeSearchGraphStore`
- `cmd.runServe_part1`
- `signal.Notify`

### Community 258: eventbus (7 nodes, cohesion=0.157)

- `eventbus.DecodePayload`
- `eventbus.NewHookBridge`
- `eventbus.TestBus_PubSub`
- `eventbus.TestHookBridge_PublishesEvents`
- `eventbus.cancel`
- `eventbus.int`
- `eventbus_test.go`

### Community 259: timing (7 nodes, cohesion=0.143)

- `timing.FormatDuration`
- `timing.ProfileTimer.TotalFormatted`
- `timing.TestFormatDurationHours`
- `timing.TestFormatDurationMinutes`
- `timing.TestFormatDurationSeconds`
- `timing.Timer.ElapsedFormatted`
- `timing.int`

### Community 260: chat_flags_test (7 nodes, cohesion=0.155)

- `cmd.RegisterChatFlags`
- `cmd.TestRegisterChatFlags_MaxTurnsDefaultIs1`
- `cmd.TestRegisterChatFlags_OutputFormatDefaultIsText`
- `cmd.TestRegisterChatFlags_RegistersAllFlags`
- `cmd.TestRegisterChatFlags_RepoDefaultIsDot`
- `cmd.TestRunHeadless_EarlyExitOnNilResponse`
- `chat_flags_test.go`

### Community 261: router_test (7 nodes, cohesion=0.143)

- `tools.UnifiedRouter.AvailableTools`
- `tools.UnifiedRouter.DescribeTools`
- `tools.append`
- `tools.mockMCPCaller.AvailableTools`
- `tools.mockMCPCaller.CallTool`
- `tools.mockMCPCallerError.AvailableTools`
- `tools.mockSkillCaller.CallSkill`

### Community 262: community (7 nodes, cohesion=0.043)

- `gleannmemory.CommunityAnalysis`
- `gleannmemory.CommunityClient`
- `gleannmemory.CommunityClient.AnalyzeFiles_part2`
- `gleannmemory.CommunityInfo`
- `gleannmemory.CrossCommunityEdge`
- `gleannmemory.symbolResult`
- `community.go`

### Community 263: planner_extended_test (7 nodes, cohesion=0.140)

- `planner.New`
- `planner.TestBuildPrompt_NoTools`
- `planner.TestBuildPrompt_WithCodeContext`
- `planner.TestBuildPrompt_WithTools`
- `planner.defaultTools`
- `cmd.TestPlannerWithMockProvider`
- `planner_extended_test.go`

### Community 264: chunked (7 nodes, cohesion=0.143)

- `intent.Detector.detectWithLLM_part1`
- `mode.Planner.buildPlanPrompt_part1`
- `planner.Planner.buildPrompt_part1`
- `reviewer.Agent.reviewChunk_part1`
- `reviewer.Agent.synthesizeChunkReviews_part1`
- `prompt.Raw`
- `prompt.TestRawConvenience`

### Community 265: doc (7 nodes, cohesion=0.043)

- `cmd.docAffectedCmd`
- `cmd.docArchitectureCmd`
- `cmd.docCmd`
- `cmd.docCommitMsgCmd`
- `cmd.docHandoverCmd`
- `cmd.docRepoPath`
- `doc.go`

### Community 266: apiserver (7 nodes, cohesion=0.190)

- `apiserver.Server.handleMetrics_part1`
- `apiserver.Server.handleStatus`
- `apiserver.len`
- `metrics.GetCollector`
- `runtime.NumGoroutine`
- `runtime.ReadMemStats`
- `runtime.Version`

### Community 267: sdd_extended_test (7 nodes, cohesion=0.148)

- `cmd.TestExtractTaskContent_ArtifactFirst`
- `cmd.TestExtractTaskContent_EmptyTask`
- `cmd.TestExtractTaskContent_HistoryLastAgent`
- `cmd.TestExtractTaskContent_StatusFallbackOnly`
- `cmd.TestExtractTaskContent_part1`
- `cmd.extractTaskContent`
- `sdd_extended_test.go`

### Community 268: chunked (7 nodes, cohesion=0.043)

- `reviewer.Agent.reviewChunk_part2`
- `reviewer.Agent.reviewChunked_part2`
- `reviewer.Agent.reviewChunked_part3`
- `reviewer.Agent.synthesizeChunkReviews_part2`
- `reviewer.Chunk`
- `reviewer.chunkSize`
- `chunked.go`

### Community 269: permission_test (7 nodes, cohesion=0.155)

- `chat.NewPermissionService`
- `chat.TestPermissionAllow`
- `chat.TestPermissionAsk`
- `chat.TestPermissionGrant`
- `chat.TestPermissionGrantAll`
- `chat.TestPermissionYolo`
- `permission_test.go`

### Community 270: dashboard (7 nodes, cohesion=0.043)

- `tui.DashboardModel`
- `tui.LogEntry`
- `tui.Phase`
- `tui.PhaseIdle`
- `tui.renderLogs_part2`
- `tui.renderLogs_part3`
- `dashboard.go`

### Community 271: permission (7 nodes, cohesion=0.043)

- `chat.PermAllow`
- `chat.PermissionLevel`
- `chat.PermissionService`
- `chat.PermissionService.GrantAll`
- `chat.PermissionService.IsYolo`
- `chat.ToolPermissionMap`
- `permission.go`

### Community 272: coverage_test (7 nodes, cohesion=0.143)

- `chat.TestExtractPlanFromContent_HeaderSteps`
- `chat.TestExtractPlanFromContent_LongDescriptionTruncated`
- `chat.TestExtractPlanFromContent_MarkdownStripping`
- `chat.TestExtractPlanFromContent_ParenNumbering`
- `chat.TestExtractPlanFromContent_SingleStep`
- `chat.TestExtractPlanFromContent_TwoDigitSteps`
- `chat.extractPlanFromContent`

### Community 273: graphstore (7 nodes, cohesion=0.043)

- `graphstore.Community`
- `graphstore.GraphStats`
- `graphstore.ImpactResult`
- `graphstore.Provider`
- `graphstore.ProviderGleann`
- `graphstore.Symbol`
- `store.go`

### Community 274: status (7 nodes, cohesion=0.043)

- `cmd.renderDashboard_part2`
- `cmd.statusCmd`
- `cmd.statusLiveCmd`
- `cmd.statusResetCmd`
- `cmd.statusShowCmd_part1`
- `cmd.statusShowCmd_part2`
- `status.go`

### Community 275: mode_test (7 nodes, cohesion=0.143)

- `mode.NewPlannerForTest`
- `mode.TestExecutionPlan_Timing`
- `mode.TestPlanStep_Defaults`
- `mode.TestPlanner_Plan_LLMErrors_Fallback`
- `mode.TestPlanner_Plan_MarkdownFencesStripped`
- `mode.TestPlanner_Plan_ValidJSON`
- `mode.TestPlanner_fallbackPlan_LongTaskTruncated`

### Community 276: store_test (7 nodes, cohesion=0.043)

- `graphstore.TestGleannClientCallees_part2`
- `graphstore.TestGleannClientCallees_part3`
- `graphstore.TestGleannClientCallees_part4`
- `graphstore.TestGleannClient_Endpoints_part2`
- `graphstore.TestGraphStoreImplementation`
- `graphstore.TestProviderConstants`
- `store_test.go`

### Community 277: pipeline_test (7 nodes, cohesion=0.143)

- `parallelreview.Merge`
- `parallelreview.TestMerge_AllApproved`
- `parallelreview.TestMerge_CriticalIssuesForceChangesRequested`
- `parallelreview.TestMerge_DurationTracking`
- `parallelreview.TestMerge_EmptyResults`
- `parallelreview.append`
- `parallelreview.float64`

### Community 278: agent (7 nodes, cohesion=0.167)

- `social.Agent.dedup`
- `social.Agent.discover_part1`
- `social.GetPendingQueue`
- `social.PendingMentionQueue.Clear`
- `social.PendingMentionQueue.GetAll`
- `social.WebhookBridge.convertToSocialItem`
- `social.make`

### Community 279: zombie (7 nodes, cohesion=0.043)

- `handlers.Base.CheckZombieReactionEnhanced_part2`
- `handlers.Base.removeReaction`
- `handlers.FileVolatility`
- `handlers.ReactionInfo`
- `handlers.UnprocessedComment`
- `handlers.ZombieTimeoutMinutes`
- `zombie.go`

### Community 280: loop_adapter_test (7 nodes, cohesion=0.126)

- `chat.NewLoopAdapter`
- `chat.TestLoopAdapterAgent`
- `chat.TestLoopAdapter_WithConfig`
- `chat.TestLoopAdapter_WithHooks`
- `chat.TestLoopAdapter_WithPermissions`
- `chat.TestNewLoopAdapter`
- `loop_adapter_extras_test.go`

### Community 281: apiserver (7 nodes, cohesion=0.043)

- `apiserver.Option`
- `apiserver.Server`
- `apiserver.Server.Handler`
- `apiserver.Server.SetStatus`
- `apiserver.Server.handleMetrics_part2`
- `apiserver.Server.registerRoutes`
- `apiserver.go`

### Community 282: logger (7 nodes, cohesion=0.083)

- `main.go`
- `yaver.main`
- `ui.InstallPrettyLogger`
- `ui.NewPrettyHandler`
- `ui.PrettyHandler`
- `ui.PrettyHandler.Enabled`
- `logger.go`

### Community 283: dispatch_test (7 nodes, cohesion=0.143)

- `chattools.NeedsApproval`
- `chattools.TestNeedsApproval`
- `chattools.TestNeedsApproval_CaseInsensitive`
- `chattools.TestNeedsApproval_DangerousShell`
- `chattools.TestNeedsApproval_DangerousWrite`
- `chattools.TestNeedsApproval_Empty`
- `chattools.TestNeedsApproval_SafeCommand`

### Community 284: warmpool (6 nodes, cohesion=0.050)

- `subagent.WarmAgent`
- `subagent.WarmPool`
- `subagent.WarmPool.AcquireOrSpawn`
- `subagent.WarmPool.Stop`
- `subagent.WarmPool.Submit`
- `warmpool.go`

### Community 285: serve (6 nodes, cohesion=0.050)

- `cmd.runServe_part2`
- `cmd.runServe_part3`
- `cmd.runServe_part4`
- `cmd.serveAddr`
- `cmd.serveCmd`
- `serve.go`

### Community 286: benchmark_print_test (6 nodes, cohesion=0.167)

- `cmd.TestDoctor_Truncate`
- `cmd.TestTruncate_Empty`
- `cmd.TestTruncate_Exact`
- `cmd.TestTruncate_Long`
- `cmd.TestTruncate_Short`
- `cmd.truncate`

### Community 287: sse (6 nodes, cohesion=0.050)

- `mcp.SSEClient`
- `mcp.SSEClient.Running`
- `mcp.SSEClient.Stop`
- `mcp.SSEEvent`
- `mcp.SSEHandler`
- `sse.go`

### Community 288: progress (6 nodes, cohesion=0.167)

- `handlers.MockForge.CreateIssueComment`
- `handlers.ProgressReporter.AddPhase`
- `handlers.ProgressReporter.Finish`
- `handlers.ProgressReporter.PostPlan`
- `handlers.append`
- `handlers.mockForgeBase.CreateIssueComment`

### Community 289: apiserver (6 nodes, cohesion=0.233)

- `apiserver.Server.Shutdown`
- `apiserver.Server.Start`
- `debug.Close`
- `debug.SetLevel`
- `debug.TestSetLevel`
- `slog.Info`

### Community 290: tui_helpers_test (6 nodes, cohesion=0.167)

- `tui.FormatAttrs`
- `tui.TestFormatAttrs_Empty`
- `tui.TestFormatAttrs_LongValue`
- `tui.TestFormatAttrs_Simple`
- `tui.TestFormatAttrs_Truncated`
- `tui.renderLogs_part1`

### Community 291: writer (6 nodes, cohesion=0.050)

- `coder.FileWriter`
- `coder.FileWriter.Apply_part2`
- `coder.extractFileBlocks_part2`
- `coder.fileBlockPatterns`
- `coder.knownLanguages`
- `writer.go`

### Community 292: registry (6 nodes, cohesion=0.050)

- `agentregistry.AgentInfo`
- `agentregistry.Registry`
- `agentregistry.Registry.DiscoverAgent`
- `agentregistry.Registry.GetSkill`
- `agentregistry.SkillRef`
- `registry.go`

### Community 293: zombie_test (6 nodes, cohesion=0.167)

- `handlers.TestIsStaleReaction_Empty`
- `handlers.TestIsStaleReaction_Fresh`
- `handlers.TestIsStaleReaction_Stale`
- `handlers.TestIsStaleReaction_Z`
- `handlers.float64`
- `handlers.isStaleReaction`

### Community 294: agentloop (6 nodes, cohesion=0.200)

- `agentloop.Run_part1`
- `agentloop.SpeculativeExecutor.Reset`
- `agentloop.applyContextCompression`
- `agentloop.handleModelError`
- `agentloop.make`
- `agentloop.toolCallSignature`

### Community 295: reviewer (6 nodes, cohesion=0.167)

- `reviewer.Agent.reviewSingle_part1`
- `reviewer.TestExtractFilePath_NoNewline`
- `reviewer.TestExtractFilePath_NoPrefix`
- `reviewer.TestExtractFilePath_WithPrefix`
- `reviewer.TestExtractFilePath_WithSpaces`
- `reviewer.extractFilePath`

### Community 296: executor (6 nodes, cohesion=0.050)

- `coder.ExecutionResult`
- `coder.Executor`
- `coder.Executor.Execute_part2`
- `coder.Executor.Execute_part3`
- `coder.Task`
- `executor.go`

### Community 297: context_status_test (6 nodes, cohesion=0.167)

- `tui.ContextStatus.RenderContextSection`
- `tui.TestUsageColor_Green`
- `tui.TestUsageColor_Orange`
- `tui.TestUsageColor_Red`
- `tui.TestUsageColor_Yellow`
- `tui.UsageColor`

### Community 298: working (6 nodes, cohesion=0.130)

- `memory.ConversationStore.GetHistory`
- `memory.WorkingMemory`
- `memory.WorkingMemory.PinFile`
- `memory.WorkingMemory.UnpinFile`
- `memory.append`
- `working.go`

### Community 299: speculative (6 nodes, cohesion=0.107)

- `agentloop.SpeculativeExecutor`
- `agentloop.SpeculativeExecutor.Collect`
- `agentloop.SpeculativeExecutor.OnChunk`
- `agentloop.delete`
- `agentloop.toolCallPattern`
- `speculative.go`

### Community 300: codeparse (6 nodes, cohesion=0.167)

- `analysis.NewParser`
- `codeparse.ParseClasses`
- `codeparse.ParseFunctions`
- `codeparse.ParseImports`
- `codeparse.Parser`
- `codeparse.TestParser_Singleton`

### Community 301: menu (6 nodes, cohesion=0.050)

- `tui.MenuItem`
- `tui.MenuModel`
- `tui.MenuModel.Init`
- `tui.MenuModel.SelectedCommand`
- `tui.MenuModel.View_part2`
- `menu.go`

### Community 302: main (6 nodes, cohesion=0.050)

- `main.go`
- `test_release_e2e.main_part2`
- `test_release_e2e.main_part3`
- `test_release_e2e.main_part4`
- `test_release_e2e.main_part5`
- `test_release_e2e.repoPath`

### Community 303: agent (6 nodes, cohesion=0.167)

- `errors.Is`
- `social.Agent.Run_part1`
- `social.Agent.processWebhookEvents`
- `social.TestAgent_Handle_Assignment_ErrSkipped`
- `social.TestAgent_Run_HandlerError_Propagates`
- `handlers.TestReviewProcess_Zombie`

### Community 304: dispatch_test (6 nodes, cohesion=0.167)

- `chattools.TestIsDestructiveRm_False`
- `chattools.TestIsDestructiveRm_RfFlag`
- `chattools.TestIsDestructiveRm_Safe`
- `chattools.TestIsDestructiveRm_SeparateFlags`
- `chattools.TestIsDestructiveRm_True`
- `chattools.isDestructiveRm`

### Community 305: clarify (6 nodes, cohesion=0.050)

- `cmd.clarifyCmd`
- `cmd.clarifyContext`
- `cmd.clarifyQuickCmd`
- `cmd.runClarify_part2`
- `cmd.runClarify_part3`
- `clarify.go`

### Community 306: solve (6 nodes, cohesion=0.050)

- `cmd.runSolveFile_part2`
- `cmd.runSolveFile_part3`
- `cmd.solveCmd`
- `cmd.solveFileCmd`
- `cmd.solveIterations`
- `solve.go`

### Community 307: helpers (6 nodes, cohesion=0.050)

- `handlers.ExtractPhaseContext_part2`
- `handlers.ExtractPhaseContext_part3`
- `handlers.makeProgressCallback_part2`
- `handlers.phaseRe`
- `handlers.reactionChecker`
- `helpers.go`

### Community 308: existing_pr_test (6 nodes, cohesion=0.167)

- `handlers.FindExistingPR`
- `handlers.TestFindExistingPR_APIError`
- `handlers.TestFindExistingPR_ByBody`
- `handlers.TestFindExistingPR_ByBranch`
- `handlers.TestFindExistingPR_ClosedPRIgnored`
- `handlers.TestFindExistingPR_NoPRs`

### Community 309: index_e2e_test (6 nodes, cohesion=0.183)

- `e2e.TestIndexManager_DefaultResolution`
- `e2e.TestIndexManager_ExplicitMapping`
- `e2e.TestIndexManager_MultiIndexResolution`
- `e2e.TestIndexManager_WorkflowRegisterAndResolve`
- `e2e.newTestManager`
- `index_e2e_test.go`

### Community 310: depth_extended_test (6 nodes, cohesion=0.167)

- `reviewer.TestTruncateForReview`
- `reviewer.TestTruncateForReviewCoverage_Coverage`
- `reviewer.TestTruncateForReview_LongString`
- `reviewer.TestTruncateForReview_ShortString`
- `reviewer.reviewSingleFile`
- `reviewer.truncateForReview`

### Community 311: webhook_social_e2e_test (6 nodes, cohesion=0.050)

- `e2e.TestWebhookSocial_IssueOpened_Chain_part2`
- `e2e.TestWebhookSocial_IssueOpened_Chain_part3`
- `e2e.TestWebhookSocial_MultipleEvents_part2`
- `e2e.TestWebhookSocial_MultipleEvents_part3`
- `e2e.TestWebhookSocial_PRComment_Chain_part2`
- `webhook_social_e2e_test.go`

### Community 312: token_budget (6 nodes, cohesion=0.050)

- `retrieval.CharsPerToken`
- `retrieval.TokenBudget`
- `retrieval.TokenBudget.CanFit`
- `retrieval.TokenBudget.Remaining`
- `retrieval.TokenBudget.UsedTokens`
- `token_budget.go`

### Community 313: atomic (6 nodes, cohesion=0.233)

- `hooks.TestRegistry_Disable`
- `hooks.TestRegistry_RegisterAndFire`
- `subagent.TestPool_SpawnAll`
- `subagent.string`
- `atomic.AddInt32`
- `atomic.LoadInt32`

### Community 314: agent_perf_test (6 nodes, cohesion=0.050)

- `cmd.TestAgentPerformance_part2`
- `cmd.TestAgentPerformance_part3`
- `cmd.TestAgentPerformance_part4`
- `cmd.TestAgentPerformance_part5`
- `cmd.TestAgentPerformance_part6`
- `agent_perf_test.go`

### Community 315: system (6 nodes, cohesion=0.050)

- `cmd.systemCheckCmd`
- `cmd.systemCmd`
- `cmd.systemSetupCmd`
- `cmd.systemStatusCmd_part1`
- `cmd.systemStatusCmd_part2`
- `system.go`

### Community 316: bootstrap_test (6 nodes, cohesion=0.050)

- `cmd.TestBootstrapper_DryRunDoesNotStart`
- `cmd.TestCheckAndPullModels_ServerDown`
- `cmd.TestCheckBinary_Exists`
- `cmd.TestCheckBinary_NotFound`
- `cmd.TestCheckService_NotRunning`
- `bootstrap_test.go`

### Community 317: extension (6 nodes, cohesion=0.167)

- `src.chat`
- `src.doctor`
- `src.join`
- `src.runInTerminal`
- `src.sendText`
- `src.show`

### Community 318: search (6 nodes, cohesion=0.050)

- `webresearch.Fetcher.Search_part2`
- `webresearch.SearchResponse`
- `webresearch.SearchResult`
- `webresearch.parseDDGResults_part2`
- `webresearch.parseDDGResults_part3`
- `search.go`

### Community 319: benchmark_extended_test (6 nodes, cohesion=0.167)

- `cmd.TestAmbiguityLabel`
- `cmd.TestAmbiguityLabel_Clear`
- `cmd.TestAmbiguityLabel_High`
- `cmd.TestAmbiguityLabel_Slight`
- `cmd.TestAmbiguityLabel_TooBroad`
- `cmd.ambiguityLabel`

### Community 320: onboarding (6 nodes, cohesion=0.167)

- `reviewer.TestGenerateReport_TruncatesLongReviews`
- `cmd.TestFindOriginalByLines_part1`
- `config.SetupWizard.printHeader`
- `config.SetupWizard.printSection`
- `ui.ProgressBar`
- `strings.Repeat`

### Community 321: benchmark_extended_test (6 nodes, cohesion=0.167)

- `cmd.TestMaskSecret`
- `cmd.TestMaskSecret_Empty`
- `cmd.TestMaskSecret_Exact4`
- `cmd.TestMaskSecret_Long`
- `cmd.TestMaskSecret_Short`
- `cmd.maskSecret`

### Community 322: menu_autocomplete_test (6 nodes, cohesion=0.167)

- `tui.NewMainMenu`
- `tui.TestMenuInit`
- `tui.TestMenuSelectedCommand_Default`
- `tui.TestMenuView_Default`
- `tui.TestMenuView_Quitting`
- `tui.TestNewMainMenu`

### Community 323: sdd_test (6 nodes, cohesion=0.117)

- `cmd.TestBuildSDDPrompt`
- `cmd.TestExtractTaskContent_part2`
- `cmd.TestSDDResult_Fields`
- `cmd.contains`
- `cmd.containsStr`
- `sdd_test.go`

### Community 324: daemon_e2e_test (6 nodes, cohesion=0.197)

- `e2e.MockForgeServer`
- `e2e.TestDaemonMode_ContextCancellation`
- `e2e.TestDaemonMode_SingleCycle`
- `e2e.saveForgeCfg`
- `daemon_e2e_test.go`
- `time.After`

### Community 325: config (6 nodes, cohesion=0.050)

- `cmd.configCmd_part1`
- `cmd.configCmd_part2`
- `cmd.configCmd_part3`
- `cmd.configSetCmd`
- `cmd.versionCmd`
- `config.go`

### Community 326: implement (6 nodes, cohesion=0.050)

- `cmd.implementCmd`
- `cmd.implementRepo`
- `cmd.runImplement_part2`
- `cmd.runImplement_part3`
- `cmd.runImplement_part4`
- `implement.go`

### Community 327: codesearch (6 nodes, cohesion=0.050)

- `cmd.executeStructuredQuery_part2`
- `cmd.executeStructuredQuery_part3`
- `cmd.handleNaturalQuery_part2`
- `cmd.handleNaturalQuery_part3`
- `cmd.structuredQuery`
- `codesearch.go`

### Community 328: chat_helpers_test (6 nodes, cohesion=0.167)

- `tui.TestStripMarkdownInline_Bold`
- `tui.TestStripMarkdownInline_Code`
- `tui.TestStripMarkdownInline_Mixed`
- `tui.TestStripMarkdownInline_NoMarkdown`
- `tui.TestStripMarkdownInline_Underscore`
- `tui.stripMarkdownInline`

### Community 329: planner (6 nodes, cohesion=0.050)

- `planner.Plan`
- `planner.Planner`
- `planner.Planner.buildPrompt_part2`
- `planner.Step`
- `planner.ToolInfo`
- `planner.go`

### Community 330: timing_test (6 nodes, cohesion=0.167)

- `timing.ParseDurationString`
- `timing.TestParseDurationRoundTrip`
- `timing.TestParseDurationString`
- `timing.TestParseDurationString_Empty`
- `timing.TestParseDurationString_Invalid`
- `strconv.ParseFloat`

### Community 331: chunked_test (6 nodes, cohesion=0.167)

- `reviewer.TestBuildPreAnalysis_AllSections`
- `reviewer.TestBuildPreAnalysis_ContextAndImpact`
- `reviewer.TestBuildPreAnalysis_Empty`
- `reviewer.TestBuildPreAnalysis_SafetyIssues`
- `reviewer.TestBuildPreAnalysis_ScannerFindings`
- `reviewer.buildPreAnalysis`

### Community 332: doctor (6 nodes, cohesion=0.050)

- `cmd.checkSEAgent_part2`
- `cmd.doctorCmd`
- `cmd.runDoctor_part2`
- `cmd.runDoctor_part3`
- `cmd.runDoctor_part4`
- `doctor.go`

### Community 333: fetcher (6 nodes, cohesion=0.050)

- `webresearch.FetchResult`
- `webresearch.Fetcher`
- `webresearch.Fetcher.Fetch_part2`
- `webresearch.Fetcher.Fetch_part3`
- `webresearch.maxBodySize`
- `fetcher.go`

### Community 334: lifecycle_fsm (6 nodes, cohesion=0.107)

- `stateless.NewStateMachine`
- `planner.PlanGraph`
- `planner.newPlanFSM`
- `planner.triggerActivate`
- `planner.triggerStepStart`
- `lifecycle_fsm.go`

### Community 335: mode_test (5 nodes, cohesion=0.200)

- `mode.NewActor`
- `mode.TestActor_Execute_AllStepsComplete`
- `mode.TestActor_Execute_EmptyPlan`
- `mode.TestActor_Execute_SkipsFailedDep`
- `mode.TestActor_Execute_WithDeps`

### Community 336: gleann_e2e_test (5 nodes, cohesion=0.200)

- `retrieval.WithGraph`
- `retrieval.WithMemoryBlocks`
- `e2e.TestGleann_FullPipeline_E2E`
- `e2e.containsStr`
- `e2e.mockGleannFullPipelineServer`

### Community 337: loop (5 nodes, cohesion=0.200)

- `reasoning.ReasoningLoop.Run_part1`
- `reasoning.ReasoningLoop.think`
- `reasoning.Reflector.reflectTemplate`
- `reasoning.TestTruncate`
- `reasoning.truncate`

### Community 338: compaction_tracker (5 nodes, cohesion=0.060)

- `agentloop.CompactionEvent`
- `agentloop.CompactionStats`
- `agentloop.CompactionTracker`
- `agentloop.CompactionTracker.Stats`
- `compaction_tracker.go`

### Community 339: benchmark_cmd_test (5 nodes, cohesion=0.060)

- `cmd.TestBenchmarkCmd_Flags`
- `cmd.TestBenchmarkCompareCmd_Registered`
- `cmd.TestBenchmarkLLMCmd_Flags`
- `cmd.TestBenchmarkListCmd_Registered`
- `benchmark_cmd_test.go`

### Community 340: reflector (5 nodes, cohesion=0.060)

- `reasoning.Lesson`
- `reasoning.Reflector`
- `reasoning.Reflector.Reflect`
- `reasoning.Reflector.reflectWithLLM_part2`
- `reflector.go`

### Community 341: token_budget (5 nodes, cohesion=0.250)

- `retrieval.GleannBackend.SearchMultiText`
- `retrieval.TokenBudget.BuildContext`
- `retrieval.TokenBudget.Sections`
- `retrieval.copy`
- `retrieval.make`

### Community 342: progress (5 nodes, cohesion=0.200)

- `chat.LoopAdapter.recordTaskMemory`
- `social.PendingMentionQueue.load`
- `handlers.ProgressReporter.editComment`
- `handlers.socialSession.AddMessage`
- `slog.Warn`

### Community 343: registry_extended_test (5 nodes, cohesion=0.060)

- `tools.TestRegistry_ToLLMDefinitions_part2`
- `tools.mockSkillCallerExtended`
- `tools.mockSkillCallerExtended.CallSkill`
- `tools.mockSkillCallerExtended.GetSkill`
- `registry_extended_test.go`

### Community 344: client_test (5 nodes, cohesion=0.200)

- `v5.PlainOpen`
- `handlers.MentionHandler.handleConflict_part1`
- `git.New`
- `git.TestNew_Invalid`
- `e2e.TestE2EDORAMetricsYaverRepo_part1`

### Community 345: src (5 nodes, cohesion=0.200)

- `src.execFileSync`
- `src.installTasks`
- `src.showErrorMessage`
- `src.showInformationMessage`
- `src.trim`

### Community 346: agent_forge_test (5 nodes, cohesion=0.200)

- `social.TestAgent_Discover_ContextCancelled`
- `social.TestAgent_Run_ContextCancelledBeforeStart`
- `social.WebhookBridge.Close`
- `social.WebhookBridge.Start`
- `social.cancel`

### Community 347: project (5 nodes, cohesion=0.060)

- `cmd.projectCmd`
- `cmd.projectDeleteCmd`
- `cmd.projectHistoryCmd`
- `cmd.projectListCmd`
- `project.go`

### Community 348: cache_test (5 nodes, cohesion=0.200)

- `prompt.NewStickyLatch`
- `prompt.TestStickyLatch_ActiveSections`
- `prompt.TestStickyLatch_Include`
- `prompt.TestStickyLatch_Reset`
- `prompt.TestStickyLatch_ShouldInclude`

### Community 349: sandbox (5 nodes, cohesion=0.200)

- `sandbox.Sandbox.ExecuteCode_part1`
- `sandbox.append`
- `sandbox.cancel`
- `sandbox.string`
- `syscall.Kill`

### Community 350: permissions (5 nodes, cohesion=0.200)

- `permissions.Service.Grant`
- `permissions.Service.isGranted`
- `permissions.TestGrantKey_NoPath`
- `permissions.TestGrantKey_WithPath`
- `permissions.grantKey`

### Community 351: border_test (5 nodes, cohesion=0.165)

- `textarea.New`
- `config.TestEnvOverride`
- `tui.TestTextareaBorderColorChange`
- `border_test.go`
- `os.Unsetenv`

### Community 352: factory_test (5 nodes, cohesion=0.195)

- `forge.NewFromRemoteURL`
- `forge.TestNewFromRemoteURL_GitHub`
- `forge.TestNewFromRemoteURL_GitLab`
- `forge.TestNewFromRemoteURL_Gitea`
- `factory_test.go`

### Community 353: extract_test (5 nodes, cohesion=0.200)

- `webresearch.TestExtractTitle_Basic`
- `webresearch.TestExtractTitle_Missing`
- `webresearch.TestExtractTitle_WithAttributes`
- `webresearch.TestExtractTitle_WithEntities`
- `webresearch.extractTitle`

### Community 354: retrieval (5 nodes, cohesion=0.200)

- `retrieval.Pipeline.GetContextForAgent`
- `retrieval.WithEpisodic`
- `retrieval.WithMaxTokens`
- `retrieval.WithSession`
- `retrieval.WithVector`

### Community 355: vscode (5 nodes, cohesion=0.060)

- `cmd.runVSCode_part2`
- `cmd.vscodeCmd`
- `cmd.vscodeTask`
- `cmd.vscodeTasksFile`
- `vscode.go`

### Community 356: social_e2e_test (5 nodes, cohesion=0.060)

- `e2e.TestSocialAssignment_part2`
- `e2e.TestSocialAssignment_part3`
- `e2e.TestSocialAssignment_part4`
- `e2e.TestSocialAssignment_part5`
- `social_e2e_test.go`

### Community 357: webhook_e2e_test (5 nodes, cohesion=0.180)

- `webhook.WithBufferSize`
- `e2e.TestWebhook_PRComment`
- `e2e.TestWebhook_RecentBuffer`
- `http.Post`
- `webhook_e2e_test.go`

### Community 358: benchmark_print_test (5 nodes, cohesion=0.200)

- `cmd.TestBenchmarkLLM_Aggregates`
- `cmd.TestComputeAggregates_AllPass`
- `cmd.TestComputeAggregates_NoTasks`
- `cmd.TestComputeAggregates_ZeroValue`
- `cmd.computeAggregates`

### Community 359: memory_decorator (5 nodes, cohesion=0.200)

- `handlers.MemoryDecorator.RecallPastDecisions`
- `handlers.MemoryDecorator.RecordDecision_part1`
- `handlers.TestExtractItemMeta`
- `handlers.TestExtractItemMeta_MissingData`
- `handlers.extractItemMeta`

### Community 360: validator (5 nodes, cohesion=0.200)

- `configvalidator.Validator.validateNeo4j`
- `configvalidator.Validator.validateOllama`
- `configvalidator.Validator.validatePaths`
- `configvalidator.Validator.validateVectorDB`
- `configvalidator.append`

### Community 361: compaction_tracker_test (5 nodes, cohesion=0.060)

- `agentloop.TestCompactionEvent_Reduction_Full`
- `agentloop.TestCompactionEvent_Reduction_Half`
- `agentloop.TestCompactionEvent_Reduction_NoTokens`
- `agentloop.TestCompactionTracker_RecordAndStats_part2`
- `compaction_tracker_test.go`

### Community 362: diff_robustness_test (5 nodes, cohesion=0.210)

- `e2e.TestDiffChangeSequences`
- `e2e.TestDiffFormatChaos`
- `e2e.TestDiffSizeStress`
- `e2e.createTempFile`
- `diff_robustness_test.go`

### Community 363: tui (5 nodes, cohesion=0.200)

- `tui.ChatModel.sendMessage_part1`
- `tui.chatFn`
- `tui.chatFnMeta`
- `tui.streamFn`
- `tui.waitForStream`

### Community 364: handlers_extended_test (5 nodes, cohesion=0.195)

- `handlers.TestDirExists_False`
- `handlers.TestDirExists_FileNotDir`
- `handlers.TestDirExists_True`
- `handlers.dirExists`
- `handlers_extended_test.go`

### Community 365: loop_adapter (5 nodes, cohesion=0.200)

- `chat.LoopAdapter.parseToolCallsFromContent`
- `chat.TestHasToolCalls`
- `chat.hasToolCalls`
- `docgen.firstLine`
- `strings.IndexByte`

### Community 366: coverage_test (5 nodes, cohesion=0.200)

- `tools.TestRegisterDefaults_FileReadMissing`
- `tools.TestRegisterDefaults_Git`
- `tools.TestRegisterDefaults_ShellWithCwd`
- `tools.TestRegistry_ResolvePath`
- `tools.WithWorkDir`

### Community 367: cmd_coverage_test (5 nodes, cohesion=0.195)

- `cmd.TestLLMRoleForSDDPhase_Code`
- `cmd.TestLLMRoleForSDDPhase_SRS`
- `cmd.TestLLMRoleForSDDPhase_Test`
- `cmd.llmRoleForSDDPhase`
- `cmd_coverage_test.go`

### Community 368: skills_test (5 nodes, cohesion=0.200)

- `skills.Registry.Count`
- `skills.TestParseYAMLList`
- `skills.TestParseYAMLMeta`
- `skills.TestSplitFrontmatter`
- `skills.len`

### Community 369: test (5 nodes, cohesion=0.200)

- `test.error`
- `test.exit`
- `test.main`
- `test.resolve`
- `test.runTests`

### Community 370: key (5 nodes, cohesion=0.200)

- `key.NewBinding`
- `key.WithKeys`
- `spinner.New`
- `tui.NewChat_part1`
- `tui.opt`

### Community 371: manager_extended_test (5 nodes, cohesion=0.200)

- `indexmanager.TestParseRemoteURL_BadURL`
- `indexmanager.TestParseRemoteURL_HTTPS`
- `indexmanager.TestParseRemoteURL_SSH`
- `indexmanager.TestParseRemoteURL_TooShortPath`
- `indexmanager.parseRemoteURL`

### Community 372: pending_queue (5 nodes, cohesion=0.060)

- `social.PendingItem`
- `social.PendingMentionQueue`
- `social.PendingOption`
- `social.pendingInstance`
- `pending_queue.go`

### Community 373: agentloop_e2e_test (5 nodes, cohesion=0.060)

- `e2e.TestAgentloopE2E_FullCycle_part2`
- `e2e.TestAgentloopE2E_FullCycle_part3`
- `e2e.TestPlanTrackerE2E_Lifecycle_part2`
- `e2e.TestPlanTrackerE2E_Lifecycle_part3`
- `agentloop_e2e_test.go`

### Community 374: registry (5 nodes, cohesion=0.200)

- `tools.Registry.ClearExternal`
- `tools.Registry.List`
- `tools.Registry.ListExternal`
- `tools.Registry.ListNative`
- `tools.make`

### Community 375: context_status_test (5 nodes, cohesion=0.200)

- `tui.FormatTokenCount`
- `tui.TestFormatTokenCount_Large`
- `tui.TestFormatTokenCount_Medium`
- `tui.TestFormatTokenCount_Small`
- `tui.float64`

### Community 376: webresearch_test (5 nodes, cohesion=0.200)

- `webresearch.Summarize`
- `webresearch.TestSummarize`
- `webresearch.TestSummarize_ErrorResult`
- `webresearch.TestSummarize_Nil`
- `webresearch.TestSummarize_WithAllFields`

### Community 377: glamour (5 nodes, cohesion=0.200)

- `glamour.NewTermRenderer`
- `glamour.WithAutoStyle`
- `glamour.WithWordWrap`
- `tui.BenchmarkRenderMessages`
- `tui.newRenderer`

### Community 378: agent_commands (5 nodes, cohesion=0.060)

- `cmd.codeCmd`
- `cmd.reviewCmd_part1`
- `cmd.reviewCmd_part2`
- `cmd.reviewCmd_part3`
- `agent_commands.go`

### Community 379: test_release_e2e (5 nodes, cohesion=0.200)

- `test_release_e2e.cancel`
- `test_release_e2e.len`
- `test_release_e2e.main_part1`
- `test_release_e2e.step`
- `test_release_e2e.string`

### Community 380: chat_comprehensive_test (5 nodes, cohesion=0.060)

- `chat.MockLLMClient`
- `chat.MockLLMClient.Chat`
- `chat.MockLLMClient.ChatWithTools`
- `chat.MockLLMClient.IsOpenAIClient`
- `chat_comprehensive_test.go`

### Community 381: filediscovery (4 nodes, cohesion=0.075)

- `agentregistry.AgentRegistration`
- `agentregistry.Registry.DiscoverFromDirectory_part2`
- `agentregistry.Registry.DiscoverFromDirectory_part3`
- `filediscovery.go`

### Community 382: tui_helpers_test (4 nodes, cohesion=0.250)

- `tui.NewDashboardLogHandler`
- `tui.TestDashboardLogHandler_Enabled`
- `tui.TestDashboardLogHandler_Handle`
- `tui.TestNewDashboardLogHandler`

### Community 383: dispatch_test (4 nodes, cohesion=0.250)

- `chattools.IsSlashCommand`
- `chattools.TestIsSlashCommand`
- `chattools.TestIsSlashCommand_False`
- `chattools.TestIsSlashCommand_True`

### Community 384: mcp (4 nodes, cohesion=0.075)

- `cmd.mcpAddr`
- `cmd.mcpCmd`
- `cmd.mcpConfigCmd`
- `mcp.go`

### Community 385: depth (4 nodes, cohesion=0.250)

- `reviewer.Scratchpad.AddFinding`
- `reviewer.Scratchpad.AddSyntaxError`
- `reviewer.TestGenerateReport_AllSections_part1`
- `reviewer.append`

### Community 386: viewport (4 nodes, cohesion=0.250)

- `viewport.New`
- `viewport.WithHeight`
- `viewport.WithWidth`
- `tui.ChatModel.Update_part1`

### Community 387: github_test (4 nodes, cohesion=0.250)

- `forge.NewGitHub`
- `forge.TestGitHub_ApiURL`
- `forge.TestGitHub_RepoPath`
- `forge.TestGitHub_SetRepo`

### Community 388: decomposer (4 nodes, cohesion=0.075)

- `coder.TaskDecomposer`
- `coder.TaskDecomposition`
- `coder.llmGenerator`
- `decomposer.go`

### Community 389: parallelreview (4 nodes, cohesion=0.250)

- `parallelreview.Pipeline.Review_part1`
- `parallelreview.cancel`
- `parallelreview.int64`
- `parallelreview.reviewer`

### Community 390: analyze (4 nodes, cohesion=0.075)

- `webresearch.Link`
- `webresearch.LinkAnalysis`
- `webresearch.reAnchor`
- `analyze.go`

### Community 391: config_test (4 nodes, cohesion=0.075)

- `config.TestValidate_EnabledAgentNoURL`
- `config.TestValidate_InvalidProtocol`
- `config.TestValidate_NoLLMBackend`
- `config_test.go`

### Community 392: visualizer (4 nodes, cohesion=0.217)

- `analyzer.MermaidClassDiagram`
- `analyzer.sanitiseMermaidName`
- `visualizer.go`
- `strings.NewReplacer`

### Community 393: compaction_tracker_test (4 nodes, cohesion=0.250)

- `agentloop.NewCompactionTracker`
- `agentloop.TestCompactionTracker_Empty`
- `agentloop.TestCompactionTracker_EventsAreIsolated`
- `agentloop.TestCompactionTracker_RecordAndStats_part1`

### Community 394: collector (4 nodes, cohesion=0.075)

- `dora.Collector`
- `dora.Metrics`
- `dora.commitInfo`
- `collector.go`

### Community 395: cache_test (4 nodes, cohesion=0.250)

- `prompt.NewCacheAwareBuilder`
- `prompt.TestCacheAwareBuilder_Build`
- `prompt.TestCacheAwareBuilder_Section`
- `prompt.TestCacheAwareBuilder_Section_Unlatched`

### Community 396: coverage_test (4 nodes, cohesion=0.075)

- `reviewer.TestCheckSafety_AllPatterns_part2`
- `reviewer.TestCheckSafety_AllPatterns_part3`
- `reviewer.TestGenerateReport_AllSections_part2`
- `coverage_test.go`

### Community 397: gitlab_errors_test (4 nodes, cohesion=0.250)

- `forge.TestGitLab_FindPRByBranch_NotFound2`
- `forge.TestGitLab_ListNotifications_ErrorPath`
- `forge.TestGitLab_MarkNotificationRead_ErrorPath`
- `forge.newGitLabTestServer2`

### Community 398: clipboard (4 nodes, cohesion=0.192)

- `coder.lookPathImpl`
- `tui.copyToClipboard`
- `clipboard.go`
- `exec.LookPath`

### Community 399: webresearch (4 nodes, cohesion=0.250)

- `webresearch.TestToolWebResearch_Empty`
- `webresearch.TestToolWebResearch_MissingQuery`
- `webresearch.ToolWebResearch`
- `webresearch.int`

### Community 400: a2a_integration_e2e_test (4 nodes, cohesion=0.075)

- `e2e.TestA2AIntegrationE2E_part2`
- `e2e.TestA2AIntegrationE2E_part3`
- `e2e.TestA2AIntegrationE2E_part4`
- `a2a_integration_e2e_test.go`

### Community 401: subagent (4 nodes, cohesion=0.250)

- `subagent.Pool.Results`
- `subagent.WarmPool.Release`
- `subagent.WarmPool.WarmUp`
- `subagent.append`

### Community 402: mcp (4 nodes, cohesion=0.250)

- `mcp.SSEClient.dispatch`
- `mcp.copy`
- `mcp.h`
- `mcp.make`

### Community 403: manager (4 nodes, cohesion=0.250)

- `indexmanager.Manager.IndexFor`
- `indexmanager.Manager.indexForLocked`
- `indexmanager.TestSanitizeIndexName`
- `indexmanager.sanitizeIndexName`

### Community 404: skills (4 nodes, cohesion=0.250)

- `skills.Registry.AddDir`
- `skills.Registry.Match`
- `skills.append`
- `regexp.Compile`

### Community 405: config (4 nodes, cohesion=0.250)

- `viper.New`
- `config.bindEnv`
- `config.load`
- `config.setDefaults`

### Community 406: extension (4 nodes, cohesion=0.250)

- `src.binary`
- `src.cfg`
- `src.get`
- `src.getConfiguration`

### Community 407: timing (4 nodes, cohesion=0.333)

- `timing.ProfileSession.LogSummary`
- `timing.ProfileTimer.LogBreakdown`
- `timing.len`
- `log.Println`

### Community 408: dashboard_test (4 nodes, cohesion=0.250)

- `tui.TestRenderLogs_Empty`
- `tui.TestRenderLogs_ManyEntries`
- `tui.TestRenderLogs_WithEntries`
- `tui.renderLogs`

### Community 409: step_executor_test (4 nodes, cohesion=0.333)

- `coder.TestBuildStepPrompt`
- `coder.TestBuildStepPrompt_NoPrevChanges`
- `coder.buildStepPrompt`
- `coder.containsStr`

### Community 410: agentloop (4 nodes, cohesion=0.250)

- `agentloop.attemptCompaction`
- `agentloop.handleModelError_part1`
- `agentloop.isMaxOutputTokens`
- `agentloop.isPromptTooLong`

### Community 411: analyze (4 nodes, cohesion=0.333)

- `webresearch.Fetcher.AnalyzeLinks`
- `webresearch.TestExtractLinks`
- `webresearch.append`
- `webresearch.extractLinks`

### Community 412: manager (4 nodes, cohesion=0.250)

- `indexmanager.Manager.ListRepos`
- `indexmanager.Manager.ListShared`
- `indexmanager.TestManager_IndexesFor_WithShared`
- `indexmanager.len`

### Community 413: errors (4 nodes, cohesion=0.075)

- `configvalidator.ErrOllamaBaseURLEmpty`
- `configvalidator.ValidationError`
- `configvalidator.ValidationError.Error`
- `errors.go`

### Community 414: watermill (4 nodes, cohesion=0.250)

- `watermill.NewSlogLogger`
- `gochannel.NewGoChannel`
- `eventbus.New`
- `eventbus.TestBus_CloseStopsPublish`

### Community 415: session (4 nodes, cohesion=0.250)

- `session.Manager.AddTag`
- `session.Manager.RemoveTag`
- `session.Manager.SearchByTag`
- `session.append`

### Community 416: router (4 nodes, cohesion=0.250)

- `tools.ExtractA2ATaskContent`
- `tools.TestExtractA2ATaskContent_Artifacts`
- `tools.TestExtractA2ATaskContent_History`
- `tools.UnifiedRouter.callA2ASkill`

### Community 417: v5 (4 nodes, cohesion=0.250)

- `v5.PlainClone`
- `handlers.Base.CloneOrOpenRepo`
- `git.Clone`
- `git.TestClone_NetworkURL_Fails`

### Community 418: helpers_test (4 nodes, cohesion=0.250)

- `handlers.TestCheckFileVolatility_DefaultThreshold`
- `handlers.TestCheckFileVolatility_NoFiles`
- `handlers.checkFileVolatility`
- `handlers.runGitLog`

### Community 419: social (4 nodes, cohesion=0.250)

- `social.StateTracker.CompleteCycle`
- `social.byte`
- `social.itoa`
- `social.string`

### Community 420: dashboard_test (4 nodes, cohesion=0.250)

- `tui.TestRenderSeparator`
- `tui.TestRenderSeparator_Large`
- `tui.TestRenderSeparator_Zero`
- `tui.renderSeparator`

### Community 421: timing_test (4 nodes, cohesion=0.250)

- `timing.EstimateHumanReviewTime`
- `timing.TestEstimateHumanReviewTimeHighManyFiles`
- `timing.TestEstimateHumanReviewTimeLow`
- `timing.TestEstimateHumanReviewTimeMedium`

### Community 422: context_status_test (4 nodes, cohesion=0.250)

- `tui.RenderContextBar`
- `tui.TestRenderContextBar_Empty`
- `tui.TestRenderContextBar_Full`
- `tui.TestRenderContextBar_MinWidth`

### Community 423: tui_helpers_test (4 nodes, cohesion=0.250)

- `tui.TestAbs_Negative`
- `tui.TestAbs_Positive`
- `tui.TestAbs_Zero`
- `tui.abs`

### Community 424: agentloop_test (4 nodes, cohesion=0.250)

- `agentloop.PartitionToolCalls`
- `agentloop.TestPartitionToolCalls_AllSafe`
- `agentloop.TestPartitionToolCalls_AllUnsafe`
- `agentloop.TestPartitionToolCalls_Mixed`

### Community 425: tools (4 nodes, cohesion=0.075)

- `cmd.analyzeCmd_part1`
- `cmd.analyzeCmd_part2`
- `cmd.analyzeCmd_part3`
- `tools.go`

### Community 426: reader_test (4 nodes, cohesion=0.250)

- `reasoning.TestCodeparseGo`
- `reasoning.TestCodeparsePython`
- `reasoning.TestCodeparseUnknownLang`
- `codeparse.ParseFile`

### Community 427: src (4 nodes, cohesion=0.250)

- `src.activate`
- `src.onDidCloseTerminal`
- `src.push`
- `src.registerCommand`

### Community 428: permissions (4 nodes, cohesion=0.075)

- `chattools.PermAllow`
- `chattools.PermissionAction`
- `chattools.PermissionConfig`
- `permissions.go`

### Community 429: extension (4 nodes, cohesion=0.075)

- `extension.ts`
- `src._internal`
- `src.gap_14`
- `src.yaverTerminal`

### Community 430: codeparse (4 nodes, cohesion=0.250)

- `analysis.DetectLanguage`
- `codeparse.DetectLanguage`
- `codeparse.TestDetectLanguage`
- `codeparse.string`

### Community 431: coder_test (4 nodes, cohesion=0.250)

- `coder.TestExtractFileBlocks`
- `coder.TestExtractFileBlocksPathTraversal`
- `coder.TestExtractFileBlocksStripRepoPrefix`
- `coder.extractFileBlocks`

### Community 432: timing (4 nodes, cohesion=0.250)

- `timing.ProfileTimer.EndStage`
- `timing.ProfileTimer.Start`
- `timing.ProfileTimer.Stop`
- `log.Printf`

### Community 433: debug (4 nodes, cohesion=0.250)

- `debug.copy`
- `debug.fileHandler.WithAttrs`
- `debug.len`
- `debug.make`

### Community 434: bench_test (4 nodes, cohesion=0.192)

- `tui.BenchmarkLooksLikeImplementation`
- `tui.TestLooksLikeImplementation`
- `tui.looksLikeImplementation`
- `bench_test.go`

### Community 435: resilience (4 nodes, cohesion=0.250)

- `circuitbreaker.NewBuilder`
- `retrypolicy.NewBuilder`
- `timeout.New`
- `llm.buildResilience`

### Community 436: zombie_test (4 nodes, cohesion=0.333)

- `handlers.ResetRetryCount`
- `handlers.TestResetRetryCount_Exported`
- `handlers.TestRetryKey_Comment`
- `handlers.retryKey`

### Community 437: loghandler (4 nodes, cohesion=0.075)

- `tui.DashboardLogHandler`
- `tui.DashboardLogHandler.Enabled`
- `tui.DashboardLogHandler.WithGroup`
- `loghandler.go`

### Community 438: community_test (4 nodes, cohesion=0.158)

- `gleannmemory.TestCommunityAnalysis_FormatForReview_CrossCommunity`
- `gleannmemory.TestCommunityAnalysis_FormatForReview_NoCross`
- `gleannmemory.containsSub`
- `community_test.go`

### Community 439: eventbus (4 nodes, cohesion=0.075)

- `eventbus.Bus`
- `eventbus.Bus.Close`
- `eventbus.Bus.Subscribe`
- `eventbus.go`

### Community 440: adapter_test (4 nodes, cohesion=0.158)

- `agentregistry.TestSkillCallerAdapter_GetSkill`
- `agentregistry._`
- `agentregistry.a2aSkill`
- `adapter_test.go`

### Community 441: coverage_test (4 nodes, cohesion=0.250)

- `reviewer.TestDirOf`
- `reviewer.TestDirOf_EdgeCases`
- `reviewer.dirOf`
- `strings.LastIndexAny`

### Community 442: context_status (4 nodes, cohesion=0.075)

- `tui.ContextStatus`
- `tui.ContextStatus.Snapshot`
- `tui.ContextStatus.Update`
- `context_status.go`

### Community 443: registry (4 nodes, cohesion=0.250)

- `agentregistry.Registry.Agents`
- `agentregistry.Registry.Refresh`
- `agentregistry.Registry.Skills`
- `agentregistry.make`

### Community 444: loop_adapter_test (4 nodes, cohesion=0.250)

- `chat.TestMakeCheckPermission_AllowsTool`
- `chat.TestMakeCheckPermission_DeniesTool`
- `chat.TestMakeFireHook_WithHooks`
- `chat.fn`

### Community 445: github_errors_test (4 nodes, cohesion=0.250)

- `forge.TestGitHub_FindPRByBranch_Error_NotFound`
- `forge.TestGitHub_ListNotifications_ErrorPath`
- `forge.TestGitHub_MarkNotificationRead_ErrorPath`
- `forge.newGitHubTestServer2`

### Community 446: mock-llm (4 nodes, cohesion=0.250)

- `mock-llm.generateResponse`
- `mock-llm.handleChat`
- `mock-llm.len`
- `rand.Int63`

### Community 447: webhook_bridge (4 nodes, cohesion=0.075)

- `social.WebhookBridge`
- `social.WebhookBridge.Events`
- `social.WebhookBridge.handleEvent`
- `webhook_bridge.go`

### Community 448: permissions (4 nodes, cohesion=0.250)

- `permissions.Service.AddAllowedPath`
- `permissions.Service.AddDeniedPath`
- `permissions.Service.recordAudit`
- `permissions.append`

### Community 449: integration (4 nodes, cohesion=0.250)

- `integration.append`
- `integration.len`
- `integration.make`
- `integration.mockMCPCaller.AvailableTools`

### Community 450: resilience (4 nodes, cohesion=0.158)

- `llm.Client.resilient`
- `llm.DefaultResilienceConfig`
- `llm.ResilienceConfig`
- `resilience.go`

### Community 451: adapter (4 nodes, cohesion=0.075)

- `agentregistry.SkillCallerAdapter`
- `agentregistry.SkillCallerAdapter.CallSkill`
- `agentregistry.SkillCallerAdapter.GetSkill`
- `adapter.go`

### Community 452: cmd_utils_test (4 nodes, cohesion=0.250)

- `cmd.TestIsWritableDir`
- `cmd.TestIsWritableDir_Nonexistent`
- `cmd.isWritableDir`
- `os.Create`

### Community 453: memory_decorator (4 nodes, cohesion=0.075)

- `handlers.MemoryDecorator`
- `handlers.MemoryDecorator.RecordDecision_part2`
- `handlers.itemMeta`
- `memory_decorator.go`

### Community 454: gleann_e2e_test (4 nodes, cohesion=0.250)

- `e2e.TestGraphStore_GleannCallees`
- `e2e.TestGraphStore_GleannCallers`
- `e2e.TestGraphStore_GleannImpact`
- `e2e.mockGleannGraphServer`

### Community 455: step_executor_test (4 nodes, cohesion=0.075)

- `coder.TestAnalysisActions`
- `coder.TestPlanStep`
- `coder.TestStepResult`
- `step_executor_test.go`

### Community 456: chat_coverage_test (4 nodes, cohesion=0.250)

- `chat.LoopAdapter.makeCallModel_part1`
- `chat.TestParseArguments_EmptyInput`
- `chat.TestParseArguments_ValidArgs`
- `chat.parseArguments`

### Community 457: configset_onboard_test (4 nodes, cohesion=0.250)

- `tui.NewOnboard`
- `tui.TestNewOnboard`
- `tui.TestOnboardView`
- `tui.TestOnboardView_Done`

### Community 458: mcp (4 nodes, cohesion=0.250)

- `cmd.defaultToolHandler`
- `cmd.runMCP`
- `cmd.stop`
- `signal.NotifyContext`

### Community 459: warmpool (4 nodes, cohesion=0.250)

- `subagent.TestTypes`
- `subagent.WarmPool.Acquire`
- `subagent.WarmPool.IdleCount`
- `subagent.len`

### Community 460: reviewer_extended_test (4 nodes, cohesion=0.250)

- `reviewer.TestAddLineNumbers_Basic`
- `reviewer.TestAddLineNumbers_Empty`
- `reviewer.TestAddLineNumbers_SingleLine`
- `reviewer.addLineNumbers`

### Community 461: skill_router (4 nodes, cohesion=0.075)

- `handlers.SkillRouteResult`
- `handlers.SkillRouter`
- `handlers.intentToSkillMapping`
- `skill_router.go`

### Community 462: recall_test (4 nodes, cohesion=0.333)

- `gleannmemory.DefaultExtractionConfig`
- `gleannmemory.NewBackgroundExtractor`
- `gleannmemory.TestBackgroundExtractor_ExtractFnError`
- `gleannmemory.TestBackgroundExtractor_StartStop`

### Community 463: gitea_test (4 nodes, cohesion=0.250)

- `forge.NewGitea`
- `forge.TestGitea_ApiURL`
- `forge.TestGitea_RepoPath`
- `forge.TestGitea_SetRepo`

### Community 464: vectorsearch (4 nodes, cohesion=0.075)

- `vectorsearch.LocalIndex`
- `vectorsearch.LocalIndex.Collection`
- `vectorsearch.Result`
- `vectorsearch.go`

### Community 465: webresearch_test (4 nodes, cohesion=0.250)

- `webresearch.SummarizeMultiple`
- `webresearch.TestSummarizeMultiple`
- `webresearch.TestSummarizeMultiple_Extended`
- `e2e.TestE2E_WebResearch_Summarize_part1`

### Community 466: session (4 nodes, cohesion=0.250)

- `rand.Read`
- `session.Manager.CreateSession`
- `session.make`
- `session.shortID`

### Community 467: sandbox (4 nodes, cohesion=0.075)

- `sandbox.Result`
- `sandbox.Sandbox`
- `sandbox.Sandbox.ExecuteCode_part2`
- `sandbox.go`

### Community 468: loghandler (4 nodes, cohesion=0.250)

- `tui.SetDashboardLogger`
- `tui.TestSetDashboardLogger`
- `tui.restore`
- `slog.SetDefault`

### Community 469: session (4 nodes, cohesion=0.250)

- `session.FuseResults`
- `session.TestFuseResults_Empty`
- `session.TestFuseResults_MultiSource`
- `session.float64`

### Community 470: analysis (3 nodes, cohesion=0.333)

- `analysis.IsCodeFile`
- `codeparse.IsCodeFile`
- `codeparse.TestIsCodeFile`

### Community 471: doc_cmd_test (3 nodes, cohesion=0.217)

- `cmd.TestDefaultRepoPath`
- `cmd.defaultRepoPath`
- `doc_cmd_test.go`

### Community 472: toolparse (3 nodes, cohesion=0.100)

- `chat.ToolCall`
- `chat.toolTagRe`
- `toolparse.go`

### Community 473: tools (3 nodes, cohesion=0.333)

- `tools.TestGetStringArg_StringType`
- `tools.float64`
- `tools.getStringArg`

### Community 474: agentloop (3 nodes, cohesion=0.333)

- `chat.LoopAdapter.makeExecuteTools_part1`
- `agentloop.ExecuteToolBatches`
- `agentloop.executor`

### Community 475: styles (3 nodes, cohesion=0.500)

- `ui.Table`
- `ui.len`
- `ui.padRight`

### Community 476: context (3 nodes, cohesion=0.333)

- `context.TODO`
- `chat.Agent.buildMessages_part1`
- `social.TestHandle_UnknownType`

### Community 477: handlers_test (3 nodes, cohesion=0.333)

- `handlers.TestExtractRepo`
- `handlers.TestExtractRepoNil`
- `handlers.extractRepo`

### Community 478: loghandler (3 nodes, cohesion=0.333)

- `tui.FormatLogTime`
- `tui.TestFormatLogTime`
- `time.Date`

### Community 479: hooks (3 nodes, cohesion=0.333)

- `hooks.Registry.RegisterTyped`
- `hooks.Registry.Unregister`
- `hooks.append`

### Community 480: timing (3 nodes, cohesion=0.333)

- `timing.ProfileTimer.GetBreakdown`
- `timing.ProfileTimer.StartStage`
- `timing.make`

### Community 481: existing_pr (3 nodes, cohesion=0.217)

- `handlers.AssignmentHandler.handleCodingTask_part1`
- `handlers.pushToExistingPR`
- `existing_pr.go`

### Community 482: mcp_test (3 nodes, cohesion=0.333)

- `mcp.TestSecondsToDuration`
- `mcp.float64`
- `mcp.secondsToDuration`

### Community 483: hook_bridge (3 nodes, cohesion=0.100)

- `eventbus.HookBridge`
- `eventbus.HookBridge.wire_part2`
- `hook_bridge.go`

### Community 484: permissions (3 nodes, cohesion=0.333)

- `permissions.Service.isPathAllowed`
- `permissions.Service.isPathDenied`
- `filepath.Match`

### Community 485: tui (3 nodes, cohesion=0.333)

- `tui.NewMenuWithItems`
- `tui.TestMenuView_WithTitle`
- `tui.TestNewMenuWithItems`

### Community 486: debug_test (3 nodes, cohesion=0.217)

- `debug.ParseLevel`
- `debug.TestParseLevel`
- `debug_test.go`

### Community 487: timing (3 nodes, cohesion=0.333)

- `timing.ProfileSession.AddTimer`
- `timing.ProfileSession.GetSummary`
- `timing.append`

### Community 488: analysis (3 nodes, cohesion=0.333)

- `analysis.TreeSitterAvailable`
- `codeparse.TestTreeSitterAvailable`
- `codeparse.TreeSitterAvailable`

### Community 489: tui_helpers_test (3 nodes, cohesion=0.333)

- `tui.TestDashboardLogHandler_ChannelFull`
- `tui.TestDashboardLogHandler_HandleLevels`
- `slog.NewRecord`

### Community 490: handler_extended_test (3 nodes, cohesion=0.217)

- `coder.TestBase64Encode`
- `coder.base64Encode`
- `handler_extended_test.go`

### Community 491: classifier (3 nodes, cohesion=0.333)

- `reasoning.TaskClassifier.Classify`
- `reasoning.TestContainsAny`
- `reasoning.containsAny`

### Community 492: report (3 nodes, cohesion=0.100)

- `reviewer.ReportGenerator`
- `reviewer.ReportGenerator.CloseReport`
- `report.go`

### Community 493: lifecycle_fsm (3 nodes, cohesion=0.333)

- `planner.StepGraph`
- `planner.TrackedPlan.Revise`
- `planner.newStepFSM`

### Community 494: history (3 nodes, cohesion=0.333)

- `history.Manager.GetAllProjects`
- `history.Manager.GetHistory`
- `history.append`

### Community 495: mock_provider (3 nodes, cohesion=0.333)

- `llm.MockProvider.ChatStream`
- `llm.MockProvider.GenerateStream`
- `llm.cb`

### Community 496: coverage_test (3 nodes, cohesion=0.333)

- `tools.TestRegisterDefaults_FileEditNotFound`
- `tools.TestRegisterDefaults_FileRead`
- `tools.mustWrite`

### Community 497: cmd (3 nodes, cohesion=0.217)

- `cmd.RunChatDirect`
- `cmd.runChat`
- `chat_stub.go`

### Community 498: main (3 nodes, cohesion=0.333)

- `mock-llm.main`
- `log.Fatal`
- `http.ListenAndServe`

### Community 499: context (3 nodes, cohesion=0.333)

- `coder.ContextBuilder.Build_part1`
- `indexmanager.Get`
- `indexmanager.Reload`

### Community 500: fetcher (3 nodes, cohesion=0.333)

- `webresearch.Fetcher.FetchMultiple`
- `webresearch.Fetcher.SearchAndFetch`
- `webresearch.make`

### Community 501: agent_forge_test (3 nodes, cohesion=0.333)

- `social.TestAgent_ProcessWebhookEvents_HandlesItem`
- `social.TestAgent_ProcessWebhookEvents_NoEvents`
- `social.newTestBridge`

### Community 502: timing_extended_test (3 nodes, cohesion=0.333)

- `timing.TestAppendUnique`
- `timing.TestAppendUnique_Extended`
- `timing.appendUnique`

### Community 503: vectorsearch (3 nodes, cohesion=0.333)

- `vectorsearch.LocalIndex.Search`
- `vectorsearch.len`
- `vectorsearch.make`

### Community 504: coder (3 nodes, cohesion=0.333)

- `coder.HandleCodingTask`
- `coder.TestHandleCodingTask_NilExecutor`
- `handlers.MentionHandler.handleCodingMention_part1`

### Community 505: step_executor_test (3 nodes, cohesion=0.333)

- `coder.TestTruncate`
- `coder.buildStepPrompt_part1`
- `coder.truncateStr`

### Community 506: dashboard_test (3 nodes, cohesion=0.333)

- `tui.TestRenderTaskPanel`
- `tui.TestRenderTaskPanel_Minimal`
- `tui.renderTaskPanel`

### Community 507: src (3 nodes, cohesion=0.333)

- `src.String`
- `src.entries`
- `src.extraEnv`

### Community 508: permissions (3 nodes, cohesion=0.333)

- `chattools.TestPermissions_Default`
- `chattools.defaultPermissions`
- `chattools.loadPermissions`

### Community 509: diff_test_harness (3 nodes, cohesion=0.217)

- `integration.string`
- `integration.verifyFileContent`
- `diff_test_harness.go`

### Community 510: cmd_utils_test (3 nodes, cohesion=0.333)

- `cmd.TestInPath`
- `cmd.inPath`
- `filepath.SplitList`

### Community 511: debug (3 nodes, cohesion=0.333)

- `debug.Logger`
- `debug.TestLogger`
- `debug.string`

### Community 512: skill_router (3 nodes, cohesion=0.333)

- `handlers.SkillRouter.deriveSkillIDs`
- `handlers.TestEscalationConstants`
- `handlers.string`

### Community 513: loop_adapter (3 nodes, cohesion=0.333)

- `chat.LoopAdapter.makeCompactor_part1`
- `chat.TestConvertRole`
- `chat.convertRole`

### Community 514: watermill (3 nodes, cohesion=0.333)

- `watermill.NewUUID`
- `message.NewMessage`
- `eventbus.Bus.Publish`

### Community 515: collector (3 nodes, cohesion=0.333)

- `metrics.Collector.RecordSelfAudit`
- `metrics.Collector.RecordTask`
- `metrics.float64`

### Community 516: skill_router (3 nodes, cohesion=0.333)

- `handlers.SkillRouter.Route`
- `handlers.TestExtractTaskContent_Artifacts`
- `handlers.extractTaskContent`

### Community 517: executor_test (3 nodes, cohesion=0.333)

- `coder.TestBuildCoderPrompt`
- `coder.TestExecutor_BuildCoderPrompt`
- `coder.buildCoderPrompt`

### Community 518: manager (3 nodes, cohesion=0.333)

- `indexmanager.Manager.LinkSharedIndex`
- `indexmanager.TestManager_SharedIndex_LinkUnlink`
- `indexmanager.contains`

### Community 519: chunked_test (3 nodes, cohesion=0.333)

- `reviewer.TestNumberLines`
- `reviewer.TestNumberLinesWithOffset`
- `reviewer.numberLines`

### Community 520: a2a (3 nodes, cohesion=0.333)

- `a2a.Server.cleanupTasks`
- `a2a.delete`
- `time.Parse`

### Community 521: handlers_test (3 nodes, cohesion=0.333)

- `handlers.TestExtractIssue`
- `handlers.TestExtractIssueInvalid`
- `handlers.extractIssue`

### Community 522: cmd_utils_test (3 nodes, cohesion=0.333)

- `cmd.TestSafeRmDir_NoForce`
- `cmd.TestSafeRmDir_NotExists`
- `cmd.safeRmDir`

### Community 523: gleann_memory_adapter (3 nodes, cohesion=0.100)

- `retrieval.MemoryBlocksAdapter`
- `retrieval.MemoryEngineAdapter`
- `gleann_memory_adapter.go`

### Community 524: prompt (3 nodes, cohesion=0.333)

- `prompt.CacheAwareBuilder.Build`
- `prompt.Registry.List`
- `prompt.append`

### Community 525: fileref (3 nodes, cohesion=0.100)

- `chattools.ExpandFileRefs_part2`
- `chattools.fileRefPattern`
- `fileref.go`

### Community 526: debug (3 nodes, cohesion=0.333)

- `debug.Init`
- `debug.IsActive`
- `debug.TestInitAndClose`

### Community 527: session (3 nodes, cohesion=0.100)

- `handlers.socialSession`
- `handlers.socialSession.Close`
- `session.go`

### Community 528: subagent (3 nodes, cohesion=0.333)

- `subagent.Pool.SpawnAll`
- `subagent.WarmPool.spawnWarm`
- `subagent.make`

### Community 529: extract (3 nodes, cohesion=0.100)

- `webresearch.extractTextFromHTML_part2`
- `webresearch.extractTextFromHTML_part3`
- `extract.go`

### Community 530: cache (3 nodes, cohesion=0.333)

- `prompt.StickyLatch.ActiveSections`
- `prompt.StickyLatch.Reset`
- `prompt.make`

### Community 531: debug (3 nodes, cohesion=0.333)

- `debug.fileHandler.Handle`
- `io.WriteString`
- `runtime.CallersFrames`

### Community 532: zombie (3 nodes, cohesion=0.333)

- `handlers.CheckFileVolatility`
- `handlers.TestCheckFileVolatility_Public`
- `handlers.countRecentCommits`

### Community 533: client (3 nodes, cohesion=0.333)

- `bufio.NewReader`
- `mcp.Client.connectStdio`
- `mcpserver.Server.ServeStdio`

### Community 534: context (3 nodes, cohesion=0.100)

- `gleannmemory.ContextInjector`
- `gleannmemory.ContextInjector.Invalidate`
- `context.go`

### Community 535: registry (3 nodes, cohesion=0.333)

- `agentregistry.Registry.StartHealthMonitor`
- `gleannmemory.BackgroundExtractor.loop`
- `time.NewTicker`

### Community 536: static_analysis_test (3 nodes, cohesion=0.333)

- `coder.NewSelfReview`
- `selfaudit.NewStaticAnalyzer`
- `selfaudit.TestRunGoVet_NonGoDir`

### Community 537: tui (3 nodes, cohesion=0.333)

- `tui.TestWithChatStreamFn`
- `tui.WithChatStreamFn`
- `tui.cb`

### Community 538: atomic (3 nodes, cohesion=0.333)

- `agentloop.TestExecuteToolBatches_Concurrent`
- `atomic.CompareAndSwapInt64`
- `atomic.LoadInt64`

### Community 539: exec (3 nodes, cohesion=0.333)

- `coder.Executor.ExecuteTool`
- `exec.Execute`
- `exec.Name`

### Community 540: collector (3 nodes, cohesion=0.333)

- `metrics.Collector.Format`
- `metrics.TestCollectorClear`
- `metrics.len`

### Community 541: eventbus (3 nodes, cohesion=0.333)

- `eventbus.HookBridge.wire_part1`
- `eventbus.len`
- `eventbus.string`

### Community 542: stdio (3 nodes, cohesion=0.100)

- `mcpserver.StdioError`
- `mcpserver.StdioMessage`
- `stdio.go`

### Community 543: token_budget (3 nodes, cohesion=0.333)

- `retrieval.TokenBudget.Summary`
- `retrieval.float64`
- `retrieval.max`

### Community 544: completion (3 nodes, cohesion=0.100)

- `cmd.completionCmd`
- `cmd.init`
- `completion.go`

### Community 545: self_review (3 nodes, cohesion=0.333)

- `coder.SelfReview.buildFileCritiquePrompt`
- `coder.SelfReview.buildFixPrompt`
- `coder.truncateContent`

### Community 546: webhook (3 nodes, cohesion=0.333)

- `webhook.Receiver.Recent`
- `webhook.copy`
- `webhook.len`

### Community 547: executor_di_test (3 nodes, cohesion=0.333)

- `coder.TestExecutor_TokenSummary`
- `coder.TestTokenSummary`
- `coder.TokenSummary`

### Community 548: chattools (3 nodes, cohesion=0.333)

- `chattools.SlashHandler.handleAsk`
- `chattools.SlashHandler.handleSearch`
- `chattools.fn`

### Community 549: social (3 nodes, cohesion=0.333)

- `social.Agent.handle_part1`
- `social.WithSubjectType`
- `social.WithSubjectURL`

### Community 550: timing_extended_test (3 nodes, cohesion=0.333)

- `timing.TestFormatMinutes_ExactHour`
- `timing.TestFormatMinutes_HoursAndMinutes`
- `timing.formatMinutes`

### Community 551: prompt (3 nodes, cohesion=0.333)

- `prompt.Registry.loadEmbedded`
- `prompt.string`
- `path.Join`

### Community 552: cmd_utils_test (3 nodes, cohesion=0.333)

- `cmd.TestEnsureDir`
- `cmd.TestEnsureDir_AlreadyExists`
- `cmd.ensureDir`

### Community 553: chat (3 nodes, cohesion=0.333)

- `v2.Tick`
- `tui.ChatModel.Init`
- `tui.ChatModel.SetNotification`

### Community 554: cache (3 nodes, cohesion=0.333)

- `prompt.CacheAwareBuilder.Section`
- `prompt.TestWrapSection`
- `prompt.WrapSection`

### Community 555: coder (3 nodes, cohesion=0.333)

- `coder.DefaultContextConfig`
- `coder.TestDefaultContextConfig`
- `coder.TestDefaultContextConfigCoverage`

### Community 556: visualizer (3 nodes, cohesion=0.333)

- `analyzer.MermaidDependencyGraph`
- `analyzer.TestVisualizer_part1`
- `analyzer.safeID`

### Community 557: mcpserver_test (3 nodes, cohesion=0.333)

- `mcpserver.TestDefaultTools`
- `mcpserver.TestServer_RegisterAndListTools`
- `mcpserver.len`

### Community 558: intent (3 nodes, cohesion=0.333)

- `intent.Detector.Detect`
- `intent.TestAnalysis_MarshalJSON`
- `intent.len`

### Community 559: tui_helpers_test (3 nodes, cohesion=0.333)

- `tui.TestDashboardLogHandler_WithAttrs`
- `tui.TestDashboardLogHandler_WithGroup`
- `slog.String`

### Community 560: logger (3 nodes, cohesion=0.333)

- `ui.PrettyHandler.Handle`
- `ui.append`
- `ui.renderLevel`

### Community 561: loop (3 nodes, cohesion=0.333)

- `reasoning.ReasoningLoop.ThinkWithTools`
- `reasoning.TestFormatTools`
- `reasoning.formatTools`

### Community 562: cmd (3 nodes, cohesion=0.333)

- `cmd.applyDiffPlan`
- `cmd.discoverTargetFiles`
- `cmd.executeCodeTaskAtomic_part1`

### Community 563: apiserver (3 nodes, cohesion=0.333)

- `apiserver.TestServer_SetStatus_Concurrent`
- `apiserver.close`
- `apiserver.make`

### Community 564: configset (3 nodes, cohesion=0.333)

- `tui.ConfigSetModel.Init`
- `tui.OnboardModel.handleEnter`
- `tui.fetchOllamaModelsTUI`

### Community 565: analyze (3 nodes, cohesion=0.333)

- `webresearch.TestExtractDomain`
- `webresearch.TestExtractDomain_Extended`
- `webresearch.extractDomain`

### Community 566: registry (3 nodes, cohesion=0.333)

- `agentregistry.Registry.AvailableAgents`
- `agentregistry.Registry.DiscoverFromConfig`
- `agentregistry.append`

### Community 567: gitlab_test (3 nodes, cohesion=0.333)

- `forge.NewGitLab`
- `forge.TestGitLab_ProjectURL`
- `forge.TestGitLab_SetRepo`

### Community 568: webresearch (3 nodes, cohesion=0.333)

- `webresearch.TestStripTags_Extended`
- `webresearch.stripTags`
- `strings.ReplaceAll`

### Community 569: speculative_test (3 nodes, cohesion=0.333)

- `agentloop.TestParseToolInput_Invalid`
- `agentloop.TestParseToolInput_Valid`
- `agentloop.parseToolInput`

### Community 570: validator_test (3 nodes, cohesion=0.333)

- `configvalidator.TestValidateConfigConvenience`
- `configvalidator.TestValidateConfigInvalid`
- `configvalidator.ValidateConfig`

### Community 571: timing (3 nodes, cohesion=0.333)

- `timing.FormatDurationSeconds`
- `timing.TestFormatDurationSecondsFloat`
- `timing.float64`

### Community 572: depth (3 nodes, cohesion=0.333)

- `reviewer.Agent.ReviewFiles`
- `reviewer.ReportGenerator.CreateConsolidatedReport`
- `reviewer.make`

### Community 573: permissions (3 nodes, cohesion=0.333)

- `permissions.Service.AuditLog`
- `permissions.copy`
- `permissions.make`

### Community 574: selfaudit (3 nodes, cohesion=0.333)

- `selfaudit.TestAuditor_AuditPatch`
- `selfaudit.make`
- `selfaudit.string`

### Community 575: webresearch_test (3 nodes, cohesion=0.333)

- `webresearch.TestDecodeHTMLEntities`
- `webresearch.TestDecodeHTMLEntities_AllEntities`
- `webresearch.decodeHTMLEntities`

### Community 576: client (3 nodes, cohesion=0.333)

- `plumbing.NewBranchReferenceName`
- `git.Client.Checkout`
- `git.Client.DefaultBranch`

### Community 577: mcpserver (3 nodes, cohesion=0.333)

- `mcpserver.DefaultTools`
- `mcpserver.FormatToolList`
- `mcpserver.TestFormatToolList`

### Community 578: codesearch (3 nodes, cohesion=0.333)

- `cmd.executeStructuredQuery_part1`
- `cmd.jsonError`
- `cmd.marshalJSON`

### Community 579: codesearch_test (3 nodes, cohesion=0.333)

- `cmd.TestExecuteStructuredQuery_Impact`
- `cmd.TestParseStructuredQuery`
- `cmd.parseStructuredQuery`

### Community 580: chat (3 nodes, cohesion=0.333)

- `chat.Agent.ChatStream_part1`
- `chat.MockLLMClient.ChatStream`
- `chat.cb`

### Community 581: compaction_tracker (3 nodes, cohesion=0.333)

- `agentloop.CompactionEvent.Reduction`
- `agentloop.CompactionStats.SuccessRate`
- `agentloop.float64`

### Community 582: logger (3 nodes, cohesion=0.333)

- `ui.formatSmartDuration`
- `ui.int`
- `ui.renderAttr`

### Community 583: gleannmemory (3 nodes, cohesion=0.333)

- `gleannmemory.MemoryBlocks.SmartRecall_part1`
- `gleannmemory.int`
- `gleannmemory.rewriteFn`

### Community 584: search_extended_test (3 nodes, cohesion=0.333)

- `webresearch.TestToJSON`
- `webresearch.TestToJSON_SearchResponse`
- `webresearch.ToJSON`

### Community 585: webresearch_test (3 nodes, cohesion=0.333)

- `webresearch.TestResolveRelativeURL`
- `webresearch.TestResolveRelativeURL_Extended`
- `webresearch.resolveRelativeURL`

### Community 586: hooks (3 nodes, cohesion=0.333)

- `hooks.Registry.Count`
- `hooks.TestHookTypes`
- `hooks.len`

### Community 587: v2 (3 nodes, cohesion=0.333)

- `v2.NewView`
- `tui.ConfigSetModel.View_part1`
- `tui.OnboardModel.View_part1`

### Community 588: mcpserver (3 nodes, cohesion=0.333)

- `mcpserver.Server.Tools`
- `mcpserver.copy`
- `mcpserver.make`

### Community 589: chat_smartrecall (3 nodes, cohesion=0.100)

- `cmd.smartRecallStaleness`
- `cmd.smartRecallTopK`
- `chat_smartrecall.go`

### Community 590: hooks (3 nodes, cohesion=0.333)

- `hooks.HookData.snapshot`
- `hooks.Registry.ListHooks`
- `hooks.make`

### Community 591: agent (3 nodes, cohesion=0.333)

- `social.Agent.RunDaemon`
- `metrics.HandlerStats.AvgDuration`
- `time.Duration`

### Community 592: tools_test (3 nodes, cohesion=0.333)

- `coder.NewWebSearchExecutor`
- `coder.TestToToolDefinition`
- `coder.TestWebSearchExecutor_Create`

### Community 593: extension (3 nodes, cohesion=0.333)

- `src.chatExplainFile`
- `src.quote`
- `src.replace`

### Community 594: chat_e2e_test (3 nodes, cohesion=0.333)

- `retrieval.WithGleannTopK`
- `e2e.TestChatE2E_RAGIntegration`
- `e2e.min`

### Community 595: chromem-go (2 nodes, cohesion=0.500)

- `chromem-go.NewPersistentDB`
- `vectorsearch.NewPersistent`

### Community 596: registry (2 nodes, cohesion=0.500)

- `tools.Registry.ToLLMDefinitions`
- `tools.buildJSONSchema`

### Community 597: llm (2 nodes, cohesion=0.150)

- `llm.LLMProvider`
- `provider.go`

### Community 598: checkpoint (2 nodes, cohesion=0.500)

- `checkpoint.Manager.Save`
- `checkpoint.append`

### Community 599: skills_test (2 nodes, cohesion=0.500)

- `skills.TestInferSource`
- `skills.inferSource`

### Community 600: gitlab (2 nodes, cohesion=0.500)

- `forge.GitLab.SetRepo`
- `url.PathEscape`

### Community 601: timing (2 nodes, cohesion=0.500)

- `timing.EstimateHumanTaskTime`
- `timing.TestEstimateHumanTaskTime`

### Community 602: loghandler (2 nodes, cohesion=0.500)

- `tui.DashboardLogHandler.WithAttrs`
- `tui.copy`

### Community 603: toolparse_test (2 nodes, cohesion=0.500)

- `chat.TestParseToolCalls_part1`
- `chat.parseToolCalls`

### Community 604: gleann_backend (2 nodes, cohesion=0.500)

- `retrieval.EpisodicQueryFunc`
- `retrieval.searchFn`

### Community 605: timing (2 nodes, cohesion=0.500)

- `timing.EndSession`
- `timing.TestEndSession_NoSession`

### Community 606: hooks (2 nodes, cohesion=0.500)

- `hooks.Registry.Fire_part1`
- `hooks.copy`

### Community 607: step_executor (2 nodes, cohesion=0.500)

- `coder.NewStepExecutor`
- `coder.opt`

### Community 608: sdd_extended_test (2 nodes, cohesion=0.500)

- `cmd.TestBuildSDDPrompt_AllPhases`
- `cmd.buildSDDPrompt`

### Community 609: context (2 nodes, cohesion=0.150)

- `reviewer.ContextBuilder`
- `context.go`

### Community 610: sdd_test (2 nodes, cohesion=0.500)

- `cmd.TestIsSDDSEPhase`
- `cmd.isSDDSEPhase`

### Community 611: social (2 nodes, cohesion=0.500)

- `social.PendingMentionQueue.Remove`
- `social.delete`

### Community 612: agentloop (2 nodes, cohesion=0.500)

- `agentloop.applyContextCompression_part1`
- `agentloop.pruneOldMessages`

### Community 613: self_review (2 nodes, cohesion=0.500)

- `coder.SelfReview.Review_part1`
- `slog.NewTextHandler`

### Community 614: handlers (2 nodes, cohesion=0.500)

- `handlers.Base.FormatFooter`
- `handlers.int`

### Community 615: onboard (2 nodes, cohesion=0.500)

- `tui.OnboardModel.probeServices`
- `tui.probeHTTPQuick`

### Community 616: cmd (2 nodes, cohesion=0.500)

- `cmd.TestTryParseJSON_part1`
- `cmd.tryParseJSON`

### Community 617: session (2 nodes, cohesion=0.500)

- `session.ClassifyQuery`
- `session.TestClassifyQuery`

### Community 618: warmpool (2 nodes, cohesion=0.500)

- `subagent.WarmPool.warmLoop`
- `subagent.execCancel`

### Community 619: social (2 nodes, cohesion=0.500)

- `social.PendingMentionQueue.Add`
- `social.opt`

### Community 620: recall (2 nodes, cohesion=0.500)

- `gleannmemory.TestClassifyMemoryType`
- `gleannmemory.classifyMemoryType`

### Community 621: lifecycle (2 nodes, cohesion=0.500)

- `planner.PlanTracker.Active`
- `planner.isTerminalPlanStatus`

### Community 622: codesearch_test (2 nodes, cohesion=0.500)

- `cmd.TestExecuteStructuredQuery_NoGraphStore`
- `cmd.executeStructuredQuery`

### Community 623: pipeline (2 nodes, cohesion=0.500)

- `parallelreview.NewBatchReviewer`
- `parallelreview.delegate`

### Community 624: toolparse_test (2 nodes, cohesion=0.150)

- `chat.TestParseToolCalls_part2`
- `toolparse_test.go`

### Community 625: compaction_tracker (2 nodes, cohesion=0.500)

- `agentloop.CompactionTracker.Events`
- `agentloop.copy`

### Community 626: loop_adapter_test (2 nodes, cohesion=0.500)

- `chat.TestMakeTokenCounter`
- `chat.counter`

### Community 627: lifecycle (2 nodes, cohesion=0.500)

- `planner.PlanTracker.Track`
- `planner.make`

### Community 628: manager (2 nodes, cohesion=0.500)

- `mcp.Manager.StopServer`
- `mcp.delete`

### Community 629: codeparse (2 nodes, cohesion=0.500)

- `codeparse.LangFromExt`
- `codeparse.TestLangFromExt`

### Community 630: loop_adapter (2 nodes, cohesion=0.500)

- `chat.LoopAdapter.ChatStreamV2_part1`
- `chat.expandFileRefs`

### Community 631: lifecycle (2 nodes, cohesion=0.500)

- `planner.PlanTracker.Remove`
- `planner.delete`

### Community 632: mcpserver (2 nodes, cohesion=0.500)

- `mcpserver.Server.RegisterTool`
- `mcpserver.append`

### Community 633: tracker (2 nodes, cohesion=0.500)

- `social.StateTracker.GetRecentItems`
- `social.copy`

### Community 634: memory (2 nodes, cohesion=0.500)

- `memory.WorkingMemory.readFile`
- `memory.string`

### Community 635: a2a (2 nodes, cohesion=0.500)

- `a2a.Server.routeSkill`
- `a2a.append`

### Community 636: seagent_check_test (2 nodes, cohesion=0.150)

- `cmd.TestCheckSEAgent_part2`
- `seagent_check_test.go`

### Community 637: extension (2 nodes, cohesion=0.500)

- `src.indexRepo`
- `src.showWarningMessage`

### Community 638: executor (2 nodes, cohesion=0.500)

- `coder.Executor.GetToolDefinitions`
- `coder.ToToolDefinition`

### Community 639: a2a_test (2 nodes, cohesion=0.500)

- `a2a.TestTaskState_Constants`
- `a2a.make`

### Community 640: plumbing (2 nodes, cohesion=0.500)

- `plumbing.NewHashReference`
- `git.Client.CreateBranch`

### Community 641: forge (2 nodes, cohesion=0.150)

- `forge.Provider`
- `provider.go`

### Community 642: debug (2 nodes, cohesion=0.500)

- `debug.writeAttr`
- `strings.ContainsAny`

### Community 643: onboarding (2 nodes, cohesion=0.500)

- `config.SetupWizard.Run`
- `config.merge`

### Community 644: agentloop_test (2 nodes, cohesion=0.500)

- `agentloop.TestInputAwareSafetyClassifier_part1`
- `agentloop.classifier`

### Community 645: agentloop_test (2 nodes, cohesion=0.500)

- `agentloop.TestDetectDeathSpiral`
- `agentloop.detectDeathSpiral`

### Community 646: extension (2 nodes, cohesion=0.500)

- `src.deactivate`
- `src.dispose`

### Community 647: client (2 nodes, cohesion=0.500)

- `git.Client.GetRemoteURL`
- `git.len`

### Community 648: chattools (2 nodes, cohesion=0.500)

- `chattools.SlashHandler.handleWebResearch`
- `chattools.float64`

### Community 649: timing (2 nodes, cohesion=0.500)

- `timing.GetCurrentSession`
- `timing.TestStartSession_EndSession`

### Community 650: self_review (2 nodes, cohesion=0.500)

- `coder.SelfReview.calculateScore`
- `coder.int`

### Community 651: client (2 nodes, cohesion=0.500)

- `git.Client.ListChangedFiles`
- `git.append`

### Community 652: agent (2 nodes, cohesion=0.500)

- `social.Agent.WithRegistry`
- `social.setSkillRouter`

### Community 653: hooks (2 nodes, cohesion=0.500)

- `hooks.Registry.Enable`
- `hooks.delete`

### Community 654: sandbox_test (2 nodes, cohesion=0.500)

- `sandbox.TestInterpreterFor`
- `sandbox.interpreterFor`

### Community 655: config_extended_test (2 nodes, cohesion=0.500)

- `config.loadDotEnvFromPath`
- `config.string`

### Community 656: sdd_test (2 nodes, cohesion=0.500)

- `cmd.TestSeSkillForPhase`
- `cmd.seSkillForPhase`

### Community 657: codeparse_test (2 nodes, cohesion=0.500)

- `codeparse.TestSplitMethodName`
- `codeparse.splitMethodName`

### Community 658: config (2 nodes, cohesion=0.500)

- `config.CompletionPaths`
- `config.TestCompletionPaths`

### Community 659: chromem-go (2 nodes, cohesion=0.500)

- `chromem-go.NewDB`
- `vectorsearch.New`

### Community 660: logger (2 nodes, cohesion=0.500)

- `ui.PrettyHandler.WithGroup`
- `ui.copy`

### Community 661: collector (2 nodes, cohesion=0.500)

- `dora.Collector.findReverts`
- `dora.append`

### Community 662: registry (2 nodes, cohesion=0.500)

- `agentregistry.Registry.discoverAgent`
- `agentregistry.cancel`

### Community 663: onboarding (2 nodes, cohesion=0.500)

- `config.SetupWizard.SetupOllama`
- `config.fetchOllamaModels`

### Community 664: timing (2 nodes, cohesion=0.500)

- `timing.StartSession`
- `timing.TestGlobalSession`

### Community 665: constants (2 nodes, cohesion=0.150)

- `config.DefaultAgentUser`
- `constants.go`

### Community 666: solve_test (2 nodes, cohesion=0.150)

- `cmd.TestSolveFileCmd_Exists`
- `solve_test.go`

### Community 667: pending_queue (2 nodes, cohesion=0.500)

- `social.PendingMentionQueue.Contains`
- `social.key`

### Community 668: retrieval (2 nodes, cohesion=0.500)

- `retrieval.CountTokens`
- `retrieval.TestCountTokens`

### Community 669: dashboard (2 nodes, cohesion=0.500)

- `tui.tickCmd`
- `tui.tickMsg`

### Community 670: decomposer (2 nodes, cohesion=0.500)

- `coder.TaskDecomposer.Decompose`
- `coder.ctx_bg`

### Community 671: cmd (2 nodes, cohesion=0.500)

- `cmd.handleCodeSearch`
- `cmd.handleNaturalQuery`

### Community 672: coverage_test (2 nodes, cohesion=0.500)

- `reasoning.TestContainsStr`
- `reasoning.containsStr`

### Community 673: vscode_test (2 nodes, cohesion=0.150)

- `cmd.TestVSCode_WriteMergesExistingTasks_part2`
- `vscode_test.go`

### Community 674: search_extended_test (2 nodes, cohesion=0.500)

- `webresearch.TestToolWebSearch_WithQuery`
- `webresearch.float64`

### Community 675: clarifier (2 nodes, cohesion=0.500)

- `clarifier.DefaultConfig`
- `clarifier.TestDefaultConfig`

### Community 676: compaction_tracker (2 nodes, cohesion=0.500)

- `agentloop.CompactionTracker.Record`
- `agentloop.append`

### Community 677: onboarding_test (2 nodes, cohesion=0.500)

- `config.TestValidateURL`
- `config.validateURL`

### Community 678: scanner_test (2 nodes, cohesion=0.500)

- `reviewer.TestScanGoIssues_Clean`
- `reviewer.scanGoIssues`

### Community 679: prompt (2 nodes, cohesion=0.500)

- `prompt.RenderTemplate`
- `prompt.TestRenderTemplateConvenience`

### Community 680: coder_test (2 nodes, cohesion=0.500)

- `coder.TestContextBuilderBuild`
- `coder.contains`

### Community 681: agentloop (2 nodes, cohesion=0.500)

- `agentloop.ensureLogger`
- `agentloop.pruneOldMessages_part1`

### Community 682: mcpserver (2 nodes, cohesion=0.500)

- `mcpserver.Server.CallTool`
- `mcpserver.handler`

### Community 683: runTest (2 nodes, cohesion=0.150)

- `runTest.ts`
- `test.trailing`

### Community 684: coder (2 nodes, cohesion=0.500)

- `coder.NewTaskDecomposer`
- `coder.TestNewTaskDecomposerNilCoverage`

### Community 685: benchmark_mock_test (2 nodes, cohesion=0.500)

- `assert.Less`
- `cmd.TestComputeAggregates_LatencyPenalty`

### Community 686: context_info (2 nodes, cohesion=0.150)

- `agentloop.ContextInfo`
- `context_info.go`

### Community 687: manager (2 nodes, cohesion=0.500)

- `indexmanager.Manager.IndexesFor`
- `indexmanager.append`

### Community 688: scanner_test (2 nodes, cohesion=0.500)

- `reviewer.TestScanPythonSecurity_Clean`
- `reviewer.scanPythonSecurity`

### Community 689: e2e (2 nodes, cohesion=0.500)

- `e2e.MockGleannServer`
- `e2e.TestGraphStore_GleannAvailable`

### Community 690: permissions (2 nodes, cohesion=0.500)

- `permissions.Mode`
- `permissions.TestModeString_All`

### Community 691: tools_test (2 nodes, cohesion=0.500)

- `coder.TestMarshalParameters`
- `coder.marshalParameters`

### Community 692: handlers (2 nodes, cohesion=0.500)

- `handlers.TestMax`
- `handlers.max`

### Community 693: src (2 nodes, cohesion=0.500)

- `src.createTerminal`
- `src.getOrCreateTerminal`

### Community 694: main (2 nodes, cohesion=0.150)

- `main.go`
- `yaver-chat.version`

### Community 695: codesearch_test (2 nodes, cohesion=0.500)

- `cmd.TestHandleNaturalQuery_WithGraphStore`
- `graphstore.GraphStore`

### Community 696: registry (2 nodes, cohesion=0.500)

- `tools.Registry.RegisterExternal`
- `slog.Debug`

### Community 697: dashboard_test (2 nodes, cohesion=0.500)

- `tui.TestRenderMetricsBar`
- `tui.renderMetricsBar`

### Community 698: self_review (2 nodes, cohesion=0.500)

- `coder.SelfReview.loggerSafe`
- `slog.New`

### Community 699: fmt (2 nodes, cohesion=0.500)

- `fmt.Sscanf`
- `config.SetupWizard.selectModel`

### Community 700: clarifier (2 nodes, cohesion=0.500)

- `clarifier.AmbiguityLevel`
- `cmd.TestAmbiguityLabel_Unknown`

### Community 701: cmd_utils_test (2 nodes, cohesion=0.500)

- `cmd.TestSafeRmFile_NoForce`
- `os.CreateTemp`

### Community 702: menu (2 nodes, cohesion=0.500)

- `tui.MenuModel.View_part1`
- `ui.GradientBanner`

### Community 703: coder (2 nodes, cohesion=0.500)

- `coder.NewBuildRunner`
- `coder.TestBuildRunnerRunAndVerify`

### Community 704: dashboard (2 nodes, cohesion=0.500)

- `tui.DashboardEvent`
- `tui.DashboardModel.Update`

### Community 705: loop_adapter (2 nodes, cohesion=0.500)

- `chat.LoopAdapter.convertToLoopMessages`
- `chat.make`

### Community 706: subagent (2 nodes, cohesion=0.500)

- `subagent.Pool.run`
- `subagent.cancel`

### Community 707: skills (2 nodes, cohesion=0.500)

- `skills.Registry.List`
- `skills.make`

### Community 708: sandbox_test (2 nodes, cohesion=0.500)

- `sandbox.TestExtensionFor`
- `sandbox.extensionFor`

### Community 709: lifecycle (2 nodes, cohesion=0.500)

- `planner.TrackedPlan.Progress`
- `planner.float64`

### Community 710: factory (2 nodes, cohesion=0.500)

- `featurefactory.Factory.Implement_part1`
- `featurefactory.len`

### Community 711: codeparse (2 nodes, cohesion=0.500)

- `codeparse.IsCodeExtension`
- `codeparse.TestIsCodeExtension`

### Community 712: v2 (2 nodes, cohesion=0.500)

- `v2.Batch`
- `tui.DashboardModel.Init`

### Community 713: dashboard (2 nodes, cohesion=0.500)

- `tui.DashboardEventMsg`
- `tui.DashboardModel.waitForEvent`

### Community 714: permissions (2 nodes, cohesion=0.500)

- `permissions.Service.Check`
- `permissions.custom`

### Community 715: onboard (2 nodes, cohesion=0.500)

- `tui.OnboardModel.viewDone`
- `tui.min`

### Community 716: onboard (2 nodes, cohesion=0.500)

- `tui.OnboardModel.Update_part1`
- `strings.Map`

### Community 717: vscode_test (2 nodes, cohesion=0.500)

- `cmd.TestVSCode_DefaultTasksShape`
- `cmd.yaverTasks`

### Community 718: onboarding (2 nodes, cohesion=0.500)

- `config.generateCobraCompletion`
- `filepath.EvalSymlinks`

### Community 719: dashboard (2 nodes, cohesion=0.500)

- `tui.DashboardModel.View`
- `ui.Subtle`

### Community 720: tui (2 nodes, cohesion=0.500)

- `tui.ChatModel.renderHistoryBlock_part1`
- `tui.renderMD`

### Community 721: models (2 nodes, cohesion=0.500)

- `models.OODAPhase.String`
- `models.string`

### Community 722: agent (2 nodes, cohesion=0.500)

- `social.Agent.classifyNotification`
- `config.AgentUser`

### Community 723: logger (2 nodes, cohesion=0.500)

- `ui.PrettyHandler.WithAttrs`
- `ui.make`

### Community 724: loop_adapter_test (2 nodes, cohesion=0.500)

- `chat.TestMakeFireHook_WithPreventingHook`
- `chat.string`

### Community 725: serve_code_task_test (2 nodes, cohesion=0.500)

- `cmd.TestFindPartialMatch_part1`
- `cmd.buildManyLines`

### Community 726: tracker (2 nodes, cohesion=0.500)

- `social.StateTracker.SuccessRate`
- `social.float64`

### Community 727: agentloop_test (2 nodes, cohesion=0.500)

- `agentloop.TestApplyToolResultBudget`
- `agentloop.applyToolResultBudget`

### Community 728: coder (2 nodes, cohesion=0.500)

- `coder.NewListDirExecutor`
- `coder.TestAllToolDefinitions`

### Community 729: token_budget (2 nodes, cohesion=0.500)

- `retrieval.TokenBudget.AddSection`
- `retrieval.truncateToTokens`

### Community 730: loop_adapter (2 nodes, cohesion=0.500)

- `chat.LoopAdapter.makeFireHook`
- `hooks.Event`

### Community 731: history (2 nodes, cohesion=0.500)

- `history.Manager.CleanupOldAnalyses`
- `history.int`

### Community 732: history (2 nodes, cohesion=0.500)

- `history.Manager.GetFileVolatility`
- `history.float64`

### Community 733: codeparse (2 nodes, cohesion=0.500)

- `codeparse.FileLang`
- `codeparse.TestFileLang`

### Community 734: handler (2 nodes, cohesion=0.500)

- `coder.BranchManager.FindUniqueBranch`
- `coder.hasPRFunc`

### Community 735: agentloop_test (2 nodes, cohesion=0.500)

- `agentloop.TestPruneOldMessages`
- `agentloop.countFn`

### Community 736: agent_cmd_test (2 nodes, cohesion=0.150)

- `cmd.TestAgentWorkCmd_Registered`
- `agent_cmd_test.go`

### Community 737: registry (2 nodes, cohesion=0.500)

- `agentregistry.Registry.HealthCheck`
- `agentregistry.delete`

### Community 738: time (1 nodes, cohesion=1.000)

- `time.Time`

### Community 739: testing (1 nodes, cohesion=1.000)

- `testing.T`

### Community 740: gochannel (1 nodes, cohesion=1.000)

- `gochannel.GoChannel`

### Community 741: ast (1 nodes, cohesion=1.000)

- `ast.Expr`

### Community 742: spinner (1 nodes, cohesion=1.000)

- `spinner.Model`

### Community 743: atomic (1 nodes, cohesion=1.000)

- `atomic.Int64`

### Community 744: strings (1 nodes, cohesion=1.000)

- `strings.Reader`

### Community 745: slog (1 nodes, cohesion=1.000)

- `slog.Attr`

### Community 746: v5 (1 nodes, cohesion=1.000)

- `v5.Repository`

### Community 747: slog (1 nodes, cohesion=1.000)

- `slog.Logger`

### Community 748: v2 (1 nodes, cohesion=1.000)

- `v2.Model`

### Community 749: http (1 nodes, cohesion=1.000)

- `http.Client`

### Community 750: exec (1 nodes, cohesion=1.000)

- `exec.Cmd`

### Community 751: sync (1 nodes, cohesion=1.000)

- `sync.RWMutex`

### Community 752: http (1 nodes, cohesion=1.000)

- `http.Handler`

### Community 753: http (1 nodes, cohesion=1.000)

- `http.Request`

### Community 754: viper (1 nodes, cohesion=1.000)

- `viper.Viper`

### Community 755: sync (1 nodes, cohesion=1.000)

- `sync.Once`

### Community 756: cobra (1 nodes, cohesion=1.000)

- `cobra.Command`

### Community 757: ast (1 nodes, cohesion=1.000)

- `ast.CallExpr`

### Community 758: analysis (1 nodes, cohesion=1.000)

- `analysis.Parser`

### Community 759: ast (1 nodes, cohesion=1.000)

- `ast.BlockStmt`

### Community 760: glamour (1 nodes, cohesion=1.000)

- `glamour.TermRenderer`

### Community 761: context (1 nodes, cohesion=1.000)

- `context.Context`

### Community 762: message (1 nodes, cohesion=1.000)

- `message.Message`

### Community 763: v2 (1 nodes, cohesion=1.000)

- `v2.KeyPressMsg`

### Community 764: color (1 nodes, cohesion=1.000)

- `color.Color`

### Community 765: http (1 nodes, cohesion=1.000)

- `http.ResponseWriter`

### Community 766: bytes (1 nodes, cohesion=1.000)

- `bytes.Buffer`

### Community 767: viewport (1 nodes, cohesion=1.000)

- `viewport.Model`

### Community 768: bufio (1 nodes, cohesion=1.000)

- `bufio.Reader`

### Community 769: io (1 nodes, cohesion=1.000)

- `io.Reader`

### Community 770: httptest (1 nodes, cohesion=1.000)

- `httptest.Server`

### Community 771: textarea (1 nodes, cohesion=1.000)

- `textarea.Model`

### Community 772: v2 (1 nodes, cohesion=1.000)

- `v2.Cmd`

### Community 773: slog (1 nodes, cohesion=1.000)

- `slog.LevelVar`

### Community 774: slog (1 nodes, cohesion=1.000)

- `slog.Level`

### Community 775: http (1 nodes, cohesion=1.000)

- `http.Server`

### Community 776: ast (1 nodes, cohesion=1.000)

- `ast.File`

### Community 777: stateless (1 nodes, cohesion=1.000)

- `stateless.StateMachine`

### Community 778: http (1 nodes, cohesion=1.000)

- `http.Response`

### Community 779: chromem-go (1 nodes, cohesion=1.000)

- `chromem-go.EmbeddingFunc`

### Community 780: v2 (1 nodes, cohesion=1.000)

- `v2.View`

### Community 781: strings (1 nodes, cohesion=1.000)

- `strings.Builder`

### Community 782: sql (1 nodes, cohesion=1.000)

- `sql.DB`

### Community 783: ast (1 nodes, cohesion=1.000)

- `ast.Node`

### Community 784: io (1 nodes, cohesion=1.000)

- `io.Writer`

### Community 785: sync (1 nodes, cohesion=1.000)

- `sync.Mutex`

### Community 786: testing (1 nodes, cohesion=1.000)

- `testing.B`

### Community 787: slog (1 nodes, cohesion=1.000)

- `slog.Record`

### Community 788: chromem-go (1 nodes, cohesion=1.000)

- `chromem-go.DB`

### Community 789: http (1 nodes, cohesion=1.000)

- `http.Header`

### Community 790: slog (1 nodes, cohesion=1.000)

- `slog.Handler`

### Community 791: sync (1 nodes, cohesion=1.000)

- `sync.WaitGroup`

### Community 792: tui (1 nodes, cohesion=1.000)

- `tui.ChatModel`

### Community 793: context (1 nodes, cohesion=1.000)

- `context.CancelFunc`

### Community 794: chromem-go (1 nodes, cohesion=1.000)

- `chromem-go.Collection`

### Community 795: io (1 nodes, cohesion=1.000)

- `io.WriteCloser`

### Community 796: http (1 nodes, cohesion=1.000)

- `http.ServeMux`

### Community 797: regexp (1 nodes, cohesion=1.000)

- `regexp.Regexp`

### Community 798: v2 (1 nodes, cohesion=1.000)

- `v2.Msg`

### Community 799: token (1 nodes, cohesion=1.000)

- `token.FileSet`

## Cross-Community Edges (Surprising Connections)

These edges connect symbols in different communities, indicating inter-module coupling.
Ranked by composite score: cross-community edges involving different packages score higher.

| From | To | Communities | Score |
|------|----|------------|-------|
| `apiserver.Server.handleMetrics_part1` | `time.Since` | 266 → 5 | 1.80 |
| `v2.NewView` | `tui.ChatModel.View_part1` | 587 → 5 | 1.80 |
| `chattools.TestSlashHandler_Remember_WithMemory` | `httptest.NewServer` | 2 → 1 | 1.50 |
| `chattools.TestSlashHandler_Remember_WithMemory` | `http.HandlerFunc` | 2 → 1 | 1.50 |
| `chattools.TestSlashHandler_Remember_WithMemory` | `json.NewEncoder` | 2 → 1 | 1.50 |
| `coder.TestNewExecutor` | `coder.len` | 73 → 13 | 1.20 |
| `coder.TestNewExecutor` | `coder.make` | 73 → 0 | 1.20 |
| `strings.ToLower` | `session.ClassifyQuery` | 0 → 617 | 1.80 |
| `reviewer.TestAnalyzeCrossFileImpacts_MultipleInSameDir` | `strings.Contains` | 239 → 2 | 1.80 |
| `strings.ToLower` | `agentloop.isMaxOutputTokens` | 0 → 410 | 1.80 |
| `reviewer.TestAnalyzeCrossFileImpacts_MultipleInSameDir` | `reviewer.NewScratchpad` | 239 → 77 | 1.20 |
| `reviewer.TestReviewRepo_WithSourceFiles` | `filepath.Join` | 10 → 3 | 1.80 |
| `reviewer.TestReviewRepo_WithSourceFiles` | `context.Background` | 10 → 2 | 1.80 |
| `reviewer.TestReviewRepo_WithSourceFiles` | `os.WriteFile` | 10 → 3 | 1.80 |
| `handlers.TestCheckZombieReactionEnhanced_Errors` | `time.Now` | 47 → 29 | 1.80 |
| `tools.TestRouter_DescribeToolsMixed` | `tools.NewRegistry` | 8 → 21 | 1.20 |
| `strings.ToLower` | `chattools.Preview` | 0 → 2 | 1.50 |
| `reviewer.makeLines` | `reviewer.make` | 174 → 572 | 1.20 |
| `reviewer.makeLines` | `strings.Repeat` | 174 → 320 | 1.80 |
| `cmd.saveState` | `os.MkdirAll` | 210 → 3 | 1.80 |

> **Tip:** Many cross-community edges between the same two communities may indicate they should be merged, or there's a missing abstraction layer.

## Suggested Questions

Based on graph structure, these questions may reveal useful insights:

1. What would break if `Background` (degree 673) were refactored?
2. Is `Contains` a genuine hub or should it be split into smaller interfaces?
3. Why do communities 'devtools' and 'client_extended_test' share cross-module edges?
4. What is the relationship between `apiserver.Server.handleMetrics_part1` and `time.Since` (surprising cross-community edge)?

