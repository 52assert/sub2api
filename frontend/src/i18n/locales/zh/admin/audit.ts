export default {
  audit: {
    title: '操作日志',
    description: '记录管理员与用户的管理面操作，请求头凭证仅保留首尾、请求体已脱敏。日志无法单条删除，全量清理需二次验证。',
    clearAll: '全部清理',
    empty: '暂无操作日志',
    loadFailed: '加载操作日志失败',
    ipBans: {
      title: '登录 IP 封禁',
      permanentPolicy: '同一 IP 在 {window}内累计登录失败达到 {threshold} 次，将永久封禁，需管理员手动解封。完整登录成功后重置失败次数；等待二次验证时不重置。',
      policy: '同一 IP 在 {window}内登录失败达到 {threshold} 次，将封禁 {duration}。完整登录成功后重置失败次数；等待二次验证时不重置。',
      permanent: '永久',
      hours: '{count} 小时',
      minutes: '{count} 分钟',
      seconds: '{count} 秒',
      empty: '暂无被封禁的登录 IP',
      failureCount: '失败次数',
      blockedAt: '封禁时间',
      blockedUntil: '解封时间',
      loadFailed: '加载登录 IP 封禁列表失败',
      unban: '解除封禁',
      unbanTitle: '解除登录 IP 封禁',
      unbanMessage: '确定解除 IP {ip} 的登录封禁并重置失败次数吗？',
      unbanSuccess: '已解除 IP {ip} 的登录封禁',
      unbanFailed: '解除登录 IP 封禁失败'
    },
    filters: {
      all: '全部',
      q: '关键字',
      qPlaceholder: '路径 / 动作 / 操作者邮箱',
      actorEmail: '操作者邮箱',
      action: '动作',
      clientIp: '客户端 IP',
      method: '请求方法',
      authMethod: '认证方式',
      result: '结果',
      resultSuccess: '成功',
      resultFailure: '失败',
      startTime: '开始时间',
      endTime: '结束时间'
    },
    columns: {
      time: '时间',
      actor: '操作者',
      action: '动作',
      method: '方法',
      result: '结果',
      clientIp: '客户端 IP',
      detail: '详情'
    },
    detail: {
      title: '操作日志详情',
      actorRole: '角色',
      methodPath: '方法 / 路径',
      latency: '耗时',
      requestId: '请求 ID',
      credential: '凭证（掩码）',
      userAgent: 'User-Agent',
      requestBody: '请求体（已脱敏）',
      extra: '附加信息'
    },
    clearConfirm: {
      title: '清理全部操作日志',
      message: '此操作将永久删除所有操作日志，且不可恢复。清理动作本身会被留痕记录。确定继续吗？',
      totpTitle: '输入二次验证码',
      totpHint: '清理操作日志需要现场验证 TOTP 验证码。',
      success: '已清理 {count} 条操作日志',
      failed: '清理操作日志失败'
    }
  }
}
