# Teams Notifications

Otto can send notifications to Microsoft Teams when PR events occur — pipeline passes, failures, review comments handled, etc.

## Setup

Otto posts Adaptive Cards to a Power Automate Workflow webhook. The old Teams "Incoming Webhook" connector was deprecated — Power Automate is the replacement.

### 1. Create a Workflow in Teams

1. Open Microsoft Teams
2. Go to the channel where you want notifications
3. Click **⋯** → **Workflows** (or go to [Power Automate](https://make.powerautomate.com))
4. Create a new workflow with the trigger: **"When a Teams webhook request is received"**
5. Add a step: **"Post adaptive card in a chat or channel"**
   - Team: select your team
   - Channel: select your channel
   - Adaptive Card: use the dynamic content from the trigger body (`attachments[0].content`)
6. Save the workflow and **copy the webhook URL**

### 2. Configure Otto

```bash
otto config set notifications.teams_webhook_url "https://prod-xx.westus.logic.azure.com/workflows/..."
```

### 3. Filter Events (Optional)

By default, otto sends all events. To limit notifications:

```bash
# Only notify on PR green and PR failed
otto config set notifications.events '["pr_green", "pr_failed"]'
```

## Events

| Event | When | Card |
|-------|------|------|
| `pr_green` | PR pipelines pass (all green) | ✅ PR Passed — title, status, fix attempts, link |
| `pr_failed` | PR fix attempts exhausted | ❌ PR Failed — title, error, fix attempts, link |
| `comment_handled` | Review comment evaluated and responded to | 💬 Comment Handled — title, decision, link |

## Card Format

Otto sends [Adaptive Cards](https://adaptivecards.io/) wrapped in the Power Automate message envelope:

```json
{
  "type": "message",
  "attachments": [{
    "contentType": "application/vnd.microsoft.card.adaptive",
    "content": {
      "type": "AdaptiveCard",
      "version": "1.4",
      "body": [
        {"type": "TextBlock", "text": "✅ PR Passed", "weight": "Bolder"},
        {"type": "FactSet", "facts": [
          {"title": "Title", "value": "Add retry logic"},
          {"title": "Status", "value": "green"},
          {"title": "Fix Attempts", "value": "2 / 5"}
        ]}
      ],
      "actions": [
        {"type": "Action.OpenUrl", "title": "Open", "url": "https://..."}
      ]
    }
  }]
}
```

## Alternative: Send to yourself via chat

If you want notifications in a 1:1 chat with yourself rather than a channel, create the workflow in your personal chat instead of a channel. The setup is the same — Power Automate supports both.
