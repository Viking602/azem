package agentruntime

func (StartRunCommand) CommandName() string { return "run.start" }

func (CreateTaskCommand) CommandName() string { return "task.create" }

func (TransitionRunCommand) CommandName() string { return "run.transition" }

func (TransitionTaskCommand) CommandName() string { return "task.transition" }

func (AdvanceRunCommand) CommandName() string { return "run.advance" }

func (DispatchTaskCommand) CommandName() string { return "task.dispatch" }

func (FanOutDispatchTaskCommand) CommandName() string { return "task.dispatch_fanout" }

func (AcquireTaskExecutionCommand) CommandName() string { return "task_execution.acquire" }

func (HeartbeatTaskExecutionCommand) CommandName() string { return "task_execution.heartbeat" }

func (ReleaseTaskExecutionCommand) CommandName() string { return "task_execution.release" }

func (AppendTaskExecutionEventCommand) CommandName() string {
	return "task_execution.event.append"
}

func (AckEnvelopeCommand) CommandName() string { return "mailbox.ack" }

func (DeadLetterCommand) CommandName() string { return "mailbox.dead_letter" }

func (WriteBlackboardItemCommand) CommandName() string { return "blackboard.write_item" }

func (SubmitTypedReportCommand) CommandName() string { return "report.submit_typed" }

func (SubmitUserInputCommand) CommandName() string { return "user_input.submit" }

func (ToolInvocation) CommandName() string { return "tool.invoke" }

func (HandoffCommand) CommandName() string { return "handoff.request" }

func (SubmitResponseOutputCommand) CommandName() string { return "response.submit_output" }

func (PublishResponseCommand) CommandName() string { return "response.publish" }

func (RequestApprovalCommand) CommandName() string { return "approval.request" }

func (DecideApprovalCommand) CommandName() string { return "approval.decide" }

func (RecoverResumeTokenCommand) CommandName() string { return "resume_token.recover" }

func (StartActionAttemptCommand) CommandName() string { return "action_attempt.start" }

func (CompleteActionAttemptCommand) CommandName() string { return "action_attempt.complete" }

func (ResolveActionAttemptCommand) CommandName() string { return "action_attempt.resolve" }

func (StartTraceSpanCommand) CommandName() string { return "trace.start" }

func (EndTraceSpanCommand) CommandName() string { return "trace.end" }
