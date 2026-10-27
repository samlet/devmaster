参考Jira开发**任务、缺陷、冲刺和版本迭代的生产管理**模块, 见参考文档: `docs/drafts/jira-like-system-draft.md`
这个模块将与`ppm`和`it-solution`项目组合应用.
相关项目:
  - [business-kernel] Structure BOM, Asset Passport, Procurement: /Users/xiaofeiwu/portals/business-kernel/README.md
	- [fi] 财务: /Users/xiaofeiwu/apps/fi/README.md
	- [wallet] 钱包服务应用: /Users/xiaofeiwu/portals/erpproto/apps/walletapp/README.md
	- [humanres] 人力资源服务(HRM): /Users/xiaofeiwu/apps/humanres/README.md
	- [approver] OA/BPM: /Users/xiaofeiwu/apps/approver/README.md
	- [relink] CRM: /Users/xiaofeiwu/portals/relink/README.md
	- [artifact] Content & Evidence Service (CMS/DMS, 合同、凭证、证明文件): /Users/xiaofeiwu/apps/artifact/README.md
	- [ppm] 项目与项目组合管理, 面向按单交付的装备制造与工程交付（ETO/MTO）场景: /Users/xiaofeiwu/apps/ppm/README.md

目前已经有一个组合解决方案:
  - [it-solution] 系统解决方案事业部的 ERP 组合层: /Users/xiaofeiwu/apps/it-solution/README.md

--------

另外就是FinOps（云财务运营）相关的部分:
  - 云成本的精细化管控。国服《魔兽世界》的部分负载跑在阿里云上，使用了ECS弹性扩展来应对开服高峰。这引入了开发阶段几乎不存在的需求：FinOps（云财务运营）。ERP或配套系统需要能按游戏、区服、甚至单个活动来归集和分摊云资源消耗，识别闲置资源，并把成本数据反馈给业务侧做优化决策。AWS专门为游戏行业提供了云成本优化指南，足见这在运营阶段的权重。
  - 是不是可以扩展`fi`项目以支持FinOps? 或者单开一个项目通过API与`fi`项目集成.
  
----------
  
技术框架: 前端用nextjs, 后端用go+ent+grpc, 工作流用temporal, 数据库用postgresql, 事件用nats-jetstream. temporal/postgresql/nats/Gitea（代码/资产管理）均已用homebrew在本地安装启用(所以开发测试阶段不需要引入docker).
用户管理和验证可用keycloak, 已下载: /Users/xiaofeiwu/workspace/srvs/keycloak-26.7.4

综合这些信息, 来评估一下这个方案, 以及方案要如何落到设计上.
