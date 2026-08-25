pub struct Labels {
    pub application: &'static str,
    pub new_conversation: &'static str,
    pub conversation: &'static str,
    pub projects: &'static str,
    pub files: &'static str,
    pub changes: &'static str,
    pub pull_requests: &'static str,
    pub run_controls: &'static str,
    pub security: &'static str,
    pub terminal: &'static str,
    pub settings: &'static str,
    pub usage: &'static str,
    pub connected: &'static str,
    pub composer: &'static str,
    pub attach: &'static str,
    pub guide: &'static str,
    pub send: &'static str,
    pub queue: &'static str,
    pub stop: &'static str,
}

pub fn labels(language: &str) -> Labels {
    if language == "zh-CN" {
        Labels {
            application: "Azem 编程智能体",
            new_conversation: "新建对话",
            conversation: "对话",
            projects: "项目",
            files: "文件",
            changes: "更改",
            pull_requests: "拉取请求",
            run_controls: "运行控制",
            security: "安全扫描",
            terminal: "终端",
            settings: "设置与扩展",
            usage: "用量与上下文",
            connected: "已连接",
            composer: "消息输入",
            attach: "附件",
            guide: "引导",
            send: "发送",
            queue: "排队",
            stop: "停止",
        }
    } else {
        Labels {
            application: "Azem coding agent",
            new_conversation: "New conversation",
            conversation: "Conversation",
            projects: "Projects",
            files: "Files",
            changes: "Changes",
            pull_requests: "Pull requests",
            run_controls: "Run controls",
            security: "Security",
            terminal: "Terminal",
            settings: "Settings and extensions",
            usage: "Usage and context",
            connected: "Connected",
            composer: "Message composer",
            attach: "Attach",
            guide: "Guide",
            send: "Send",
            queue: "Queue",
            stop: "Stop",
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
