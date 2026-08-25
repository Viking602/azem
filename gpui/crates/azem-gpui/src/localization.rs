#[derive(Clone, Copy)]
pub struct Labels {
    pub application: &'static str,
    pub new_conversation: &'static str,
    pub conversation: &'static str,
    pub workspace: &'static str,
    pub search: &'static str,
    pub projects: &'static str,
    pub files: &'static str,
    pub changes: &'static str,
    pub pull_requests: &'static str,
    pub run_controls: &'static str,
    pub security: &'static str,
    pub terminal: &'static str,
    pub settings: &'static str,
    pub usage: &'static str,
    pub composer: &'static str,
    pub attach: &'static str,
    pub guide: &'static str,
    pub send: &'static str,
    pub queue: &'static str,
    pub stop: &'static str,
    pub prompt_title: &'static str,
    pub prompt_subtitle: &'static str,
    pub auto_review: &'static str,
    pub plan: &'static str,
}

pub fn labels(language: &str) -> Labels {
    if language == "zh-CN" {
        Labels {
            application: "Azem 编程智能体",
            new_conversation: "新建对话",
            conversation: "对话",
            workspace: "工作区",
            search: "搜索",
            projects: "项目",
            files: "文件",
            changes: "更改",
            pull_requests: "拉取请求",
            run_controls: "运行控制",
            security: "安全扫描",
            terminal: "终端",
            settings: "设置与扩展",
            usage: "用量与上下文",
            composer: "消息输入",
            attach: "附件",
            guide: "引导",
            send: "发送",
            queue: "排队",
            stop: "停止",
            prompt_title: "准备开始什么？",
            prompt_subtitle: "选择一个项目，然后描述要交给 Azem 完成的任务。",
            auto_review: "自动审查",
            plan: "计划",
        }
    } else {
        Labels {
            application: "Azem coding agent",
            new_conversation: "New conversation",
            conversation: "Conversation",
            workspace: "Workspace",
            search: "Search",
            projects: "Projects",
            files: "Files",
            changes: "Changes",
            pull_requests: "Pull requests",
            run_controls: "Run controls",
            security: "Security",
            terminal: "Terminal",
            settings: "Settings and extensions",
            usage: "Usage and context",
            composer: "Message composer",
            attach: "Attach",
            guide: "Guide",
            send: "Send",
            queue: "Queue",
            stop: "Stop",
            prompt_title: "What should we work on?",
            prompt_subtitle: "Choose a project, then describe the task for Azem.",
            auto_review: "Auto review",
            plan: "Plan",
        }
    }
}

#[cfg(test)]
mod tests {
    use super::labels;

    #[test]
    fn selects_english_and_chinese_labels() {
        assert_eq!(labels("en").new_conversation, "New conversation");
        assert_eq!(labels("zh-CN").new_conversation, "新建对话");
        assert_eq!(labels("unknown").settings, "Settings and extensions");
    }
}
