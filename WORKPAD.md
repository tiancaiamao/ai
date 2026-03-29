## Workpad

```
localhost:/Users/genius/.symphony/workspaces/e8e93c29-a0af-4903-bf64-b9ef3127ee7f@bd57dc3
```

### Plan

- [ ] 1. Investigate and reproduce the issue
  - [x] Read main.go and identify the SecurityConfig struct
  - [x] Confirm that Feishu AppSecret loading is missing
  - [x] Find where cfg.Channels.Feishu.AppSecret() is called
- [ ] 2. Implement the fix
  - [ ] Add Feishu field to SecurityConfig struct
  - [ ] Add Feishu AppSecret loading logic
- [ ] 3. Test and validate
  - [ ] Build the code to verify no syntax errors
  - [ ] Verify the fix logic is correct
- [ ] 4. Commit and push
  - [ ] Commit changes
  - [ ] Push branch
  - [ ] Create PR

### Acceptance Criteria

- [ ] SecurityConfig struct includes Feishu field with AppSecret
- [ ] Feishu AppSecret is loaded from .security.yml
- [ ] Code compiles without errors
- [ ] PR is created and labeled with symphony

### Validation

- [ ] Build test: `cd claw && go build ./cmd/aiclaw`
- [ ] Verify no compilation errors

### Notes

- Issue found in claw/cmd/aiclaw/main.go lines 118-142 (security loading section)
- Feishu AppSecret is used on lines 247-248 but never loaded
- Need to add Feishu field to SecurityConfig struct and loading logic similar to Pico/Weixin

### Implementation Details

Current SecurityConfig (lines 121-131):
```go
type SecurityConfig struct {
    Channels struct {
        Pico struct {
            Token string `json:"token" yaml:"token"`
        } `json:"pico" yaml:"pico"`
        Weixin struct {
            Token string `json:"token" yaml:"token"`
        } `json:"weixin" yaml:"weixin"`
    } `json:"channels" yaml:"channels"`
}
```

Add Feishu field:
```go
Feishu struct {
    AppSecret string `json:"app_secret" yaml:"app_secret"`
} `json:"feishu" yaml:"feishu"`
```

Add loading logic (after Weixin):
```go
if sec.Channels.Feishu.AppSecret != "" {
    picoCfg.Channels.Feishu.SetAppSecret(sec.Channels.Feishu.AppSecret)
    slog.Info("Loaded Feishu AppSecret from security file")
}
```