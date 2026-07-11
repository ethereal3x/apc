package ratelimit

import "context"

// Rule 分组限流中的单条规则，Key 回调返回空字符串时跳过该规则
type Rule struct {
	Name   string
	Key    func(ctx context.Context) string
	Config LimitConfig
}

// RuleGroup 多规则限流器，依次检查所有规则，任一拒绝则拒绝
type RuleGroup struct {
	limiter *Limiter
	rules   []Rule
}

// NewRuleGroup 创建多规则限流器
func NewRuleGroup(limiter *Limiter, rules []Rule) *RuleGroup {
	return &RuleGroup{limiter: limiter, rules: rules}
}

// Check 依次检查所有规则，返回首个拒绝规则的 Name，全部通过返回空字符串
func (group *RuleGroup) Check(ctx context.Context) (deniedRule string, err error) {
	for _, rule := range group.rules {
		key := rule.Key(ctx)
		if key == "" {
			continue
		}
		result, err := group.limiter.Allow(ctx, key, rule.Config)
		if err != nil {
			return "", err
		}
		if !result.Allowed {
			return rule.Name, nil
		}
	}
	return "", nil
}
