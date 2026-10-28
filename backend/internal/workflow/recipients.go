package workflow

// 通知收件人键的约定，与 notify 的 recipient_id 契约一致：
//
//	user:<anchor_id>   主体目录解析出的具体人（跨系统稳定）
//	role:<CODE>        角色收件箱（有该角色的人都能看到）
//	team:<name>        团队收件箱（notify 配置里的默认收件人）
//	customer:<code>    外部客户（it-solution 在用）
//
// 只有 user: 需要主体目录。角色与团队是业务概念，没有跨系统同一性问题。
const (
	// subjectSourceKeycloak 是 devmaster 用户主体的来源系统键：
	// Principal.Subject 直接取自 Keycloak 的 sub 声明。
	subjectSourceKeycloak = "keycloak"
	// userRecipientPrefix 是主体锚点的收件人前缀。
	//
	// 用 anchor_id 而不是 Keycloak sub：换身份提供方后 anchor 不变，
	// notify 侧的频控计数、退订记录、收件箱历史不会集体失效。
	userRecipientPrefix = "user:"
	// subjectSourceApprover 是 approver 系统的用户 id 键。审批人可能先在
	// registry 里以 approver 用户身份登记过，也可能直接用 OIDC 主体。
	subjectSourceApprover = "approver"
	// approvalRoleRecipient 是催办的收件人。
	//
	// 为什么不是具体审批人：approver 的实例接口不暴露 assignee，审批完成前
	// devmaster 拿不到审批人 id。发给角色是当前能给出的最准确答案——
	// 猜一个人比发给角色更糟，猜错就是信息泄露。
	approvalRoleRecipient = "role:RELEASE_APPROVER"
)
