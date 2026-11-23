import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import RegisterView from '@/views/auth/RegisterView.vue'

const {
  getPublicSettingsMock,
  registerMock,
  validateInvitationCodeMock,
  validatePromoCodeMock,
  showErrorMock,
  pushMock,
  verifyActionMock,
  appStoreMock
} = vi.hoisted(() => ({
  getPublicSettingsMock: vi.fn(),
  registerMock: vi.fn(),
  validateInvitationCodeMock: vi.fn(),
  validatePromoCodeMock: vi.fn(),
  showErrorMock: vi.fn(),
  pushMock: vi.fn(),
  verifyActionMock: vi.fn(),
  appStoreMock: {
    cachedPublicSettings: null as { promo_code_enabled?: boolean } | null,
    showError: (...args: unknown[]) => showErrorMock(...args),
    showSuccess: vi.fn(),
    showWarning: vi.fn()
  }
}))

const publicSettings = {
  registration_enabled: true,
  email_verify_enabled: false,
  promo_code_enabled: false,
  invitation_code_enabled: false,
  affiliate_enabled: true,
  turnstile_enabled: true,
  turnstile_site_key: 'site-key',
  site_name: 'Sub2API',
  registration_email_suffix_whitelist: [],
  linuxdo_oauth_enabled: false,
  wechat_oauth_enabled: false,
  oidc_oauth_enabled: false,
  github_oauth_enabled: false,
  google_oauth_enabled: false
}

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: pushMock }),
  useRoute: () => ({ query: {} })
}))

vi.mock('vue-i18n', () => ({
  createI18n: () => ({
    global: {
      t: (key: string) => key
    }
  }),
  useI18n: () => ({
    t: (key: string) =>
      key === 'auth.emailDomainRegistrationLimit'
        ? '该邮箱域名无法注册新账户。请使用主流邮箱注册；如需使用企业邮箱，请联系客服添加域名白名单。'
        : key,
    locale: { value: 'en' }
  })
}))

vi.mock('@/stores', () => ({
  useAuthStore: () => ({ register: (...args: unknown[]) => registerMock(...args) }),
  useAppStore: () => appStoreMock
}))

vi.mock('@/api/auth', async () => {
  const actual = await vi.importActual<typeof import('@/api/auth')>('@/api/auth')
  return {
    ...actual,
    getPublicSettings: (...args: unknown[]) => getPublicSettingsMock(...args),
    validateInvitationCode: (...args: unknown[]) => validateInvitationCodeMock(...args),
    validatePromoCode: (...args: unknown[]) => validatePromoCodeMock(...args)
  }
})

function mountRegister() {
  return mount(RegisterView, {
    global: {
      stubs: {
        AuthLayout: { template: '<div><slot /><slot name="footer" /></div>' },
        Icon: true,
        TurnstileWidget: {
          template: '<div data-testid="turnstile-widget" />',
          methods: { verifyAction: verifyActionMock, reset: vi.fn() }
        },
        LoginAgreementPrompt: true,
        EmailOAuthButtons: true,
        LinuxDoOAuthSection: true,
        WechatOAuthSection: true,
        OidcOAuthSection: true,
        RouterLink: true,
        transition: false
      }
    }
  })
}

describe('RegisterView', () => {
  beforeEach(() => {
    vi.useRealTimers()
    getPublicSettingsMock.mockReset()
    registerMock.mockReset()
    validateInvitationCodeMock.mockReset()
    validatePromoCodeMock.mockReset()
    showErrorMock.mockReset()
    pushMock.mockReset()
    verifyActionMock.mockReset()
    appStoreMock.cachedPublicSettings = null
    sessionStorage.removeItem('register_data')
    verifyActionMock.mockResolvedValue({ token: 'ticket', randstr: 'randstr' })
    getPublicSettingsMock.mockResolvedValue(publicSettings)
    registerMock.mockResolvedValue({})
    validateInvitationCodeMock.mockResolvedValue({ valid: true })
    validatePromoCodeMock.mockResolvedValue({ valid: true, bonus_amount: 0 })
  })

  it('does not flash the promo-code field before disabled settings finish loading', async () => {
    let resolveSettings!: (settings: typeof publicSettings) => void
    getPublicSettingsMock.mockReturnValueOnce(
      new Promise<typeof publicSettings>((resolve) => {
        resolveSettings = resolve
      })
    )

    const wrapper = mountRegister()

    expect(wrapper.find('#promo_code').exists()).toBe(false)

    resolveSettings(publicSettings)
    await flushPromises()

    expect(wrapper.find('#promo_code').exists()).toBe(false)
  })

  it('uses injected public settings to show an enabled promo-code field on first render', () => {
    appStoreMock.cachedPublicSettings = { promo_code_enabled: true }
    getPublicSettingsMock.mockReturnValueOnce(new Promise(() => {}))

    const wrapper = mountRegister()

    expect(wrapper.find('#promo_code').exists()).toBe(true)
  })

  it.each([
    ['', 'auth.confirmPasswordRequired'],
    ['different-password', 'auth.passwordsDoNotMatch']
  ])('blocks invalid confirmation %j before captcha and allows correction', async (confirmation, error) => {
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      turnstile_enabled: false,
      tencent_captcha_enabled: true,
      tencent_captcha_app_id: 'app-id'
    })
    const wrapper = mountRegister()
    await flushPromises()
    await wrapper.get('#email').setValue('user@example.com')
    await wrapper.get('#password').setValue('secret-123')
    await wrapper.get('#confirmPassword').setValue(confirmation)
    await wrapper.get('form').trigger('submit.prevent')
    await flushPromises()

    expect(showErrorMock).toHaveBeenCalledWith(error)
    expect(wrapper.get('#confirmPassword').classes()).toContain('input-error')
    expect(registerMock).not.toHaveBeenCalled()
    expect(verifyActionMock).not.toHaveBeenCalled()
    expect(pushMock).not.toHaveBeenCalled()
    expect(sessionStorage.getItem('register_data')).toBeNull()

    await wrapper.get('#confirmPassword').setValue('secret-123')
    await wrapper.get('form').trigger('submit.prevent')
    await flushPromises()

    expect(wrapper.get('#confirmPassword').classes()).not.toContain('input-error')
    expect(verifyActionMock).toHaveBeenCalledOnce()
    expect(registerMock).toHaveBeenCalledWith({
      email: 'user@example.com',
      password: 'secret-123',
      turnstile_token: undefined,
      tencent_captcha_ticket: 'ticket',
      tencent_captcha_randstr: 'randstr',
      promo_code: undefined,
      invitation_code: undefined
    })
    expect(pushMock).toHaveBeenCalledWith('/dashboard')
  })

  it('requires matching confirmation before storing only the registration fields for email verification', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      turnstile_enabled: false,
      email_verify_enabled: true
    })
    const wrapper = mountRegister()
    await flushPromises()
    await wrapper.get('#email').setValue('user@example.com')
    await wrapper.get('#password').setValue('secret-123')
    await wrapper.get('#confirmPassword').setValue('different-password')
    await wrapper.get('form').trigger('submit.prevent')
    await flushPromises()

    expect(sessionStorage.getItem('register_data')).toBeNull()
    expect(pushMock).not.toHaveBeenCalled()

    await wrapper.get('#confirmPassword').setValue('secret-123')
    await wrapper.get('form').trigger('submit.prevent')
    await flushPromises()

    expect(JSON.parse(sessionStorage.getItem('register_data')!)).toEqual({
      email: 'user@example.com',
      password: 'secret-123'
    })
    expect(pushMock).toHaveBeenCalledWith('/email-verify')
    expect(registerMock).not.toHaveBeenCalled()
  })

  it('keeps the optional affiliate invitation field before Turnstile', async () => {
    const wrapper = mountRegister()
    await flushPromises()

    const invitationField = wrapper.get('[data-testid="affiliate-invitation-field"]')
    const turnstile = wrapper.get('[data-testid="registration-turnstile"]')

    expect(invitationField.get('input').attributes('id')).toBe('affiliate_code')
    expect(invitationField.text()).toContain('common.optional')
    expect(
      invitationField.element.compareDocumentPosition(turnstile.element) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy()
  })

  it('uses the mandatory invitation field without duplicating the affiliate field', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      invitation_code_enabled: true
    })

    const wrapper = mountRegister()
    await flushPromises()

    expect(wrapper.find('[data-testid="affiliate-invitation-field"]').exists()).toBe(false)
    expect(wrapper.get('#invitation_code').exists()).toBe(true)
  })

  it('submits a non-whitelist email domain so the backend can enforce its registration quota', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      turnstile_enabled: false,
      registration_email_suffix_whitelist: ['allowed.com'],
      registration_email_domain_quota_enabled: true
    })

    const wrapper = mountRegister()
    await flushPromises()
    await wrapper.get('#email').setValue('first@custom.example')
    await wrapper.get('#password').setValue('secret-123')
    await wrapper.get('#confirmPassword').setValue('secret-123')
    await wrapper.get('form').trigger('submit.prevent')
    await flushPromises()

    expect(registerMock).toHaveBeenCalledWith(
      expect.objectContaining({ email: 'first@custom.example' })
    )
    expect(showErrorMock).not.toHaveBeenCalled()
  })

  it('shows the localized registration domain quota message returned by the backend', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      turnstile_enabled: false,
      registration_email_suffix_whitelist: ['allowed.com'],
      registration_email_domain_quota_enabled: true
    })
    registerMock.mockRejectedValueOnce({
      reason: 'EMAIL_DOMAIN_REGISTRATION_LIMIT',
      message: 'raw backend message'
    })

    const wrapper = mountRegister()
    await flushPromises()
    await wrapper.get('#email').setValue('second@custom.example')
    await wrapper.get('#password').setValue('secret-123')
    await wrapper.get('#confirmPassword').setValue('secret-123')
    await wrapper.get('form').trigger('submit.prevent')
    await flushPromises()

    expect(showErrorMock).toHaveBeenCalledWith(
      '该邮箱域名无法注册新账户。请使用主流邮箱注册；如需使用企业邮箱，请联系客服添加域名白名单。'
    )
  })

  // 域名限量注册开关默认关闭：恢复 PR5423 之前的客户端白名单预检，非白名单域名不发起注册请求。
  it('rejects a non-whitelist email domain locally when the domain quota switch is disabled', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      turnstile_enabled: false,
      registration_email_suffix_whitelist: ['allowed.com']
    })

    const wrapper = mountRegister()
    await flushPromises()
    await wrapper.get('#email').setValue('first@custom.example')
    await wrapper.get('#password').setValue('secret-123')
    await wrapper.get('#confirmPassword').setValue('secret-123')
    await wrapper.get('form').trigger('submit.prevent')
    await flushPromises()

    expect(registerMock).not.toHaveBeenCalled()
    // 校验失败通过 validationToastMessage watcher 弹 toast
    expect(showErrorMock).toHaveBeenCalledWith('auth.emailSuffixNotAllowedWithAllowed')
    expect(wrapper.get('#email').classes()).toContain('input-error')
  })

  it('still submits whitelisted email domains when the domain quota switch is disabled', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      turnstile_enabled: false,
      registration_email_suffix_whitelist: ['allowed.com']
    })

    const wrapper = mountRegister()
    await flushPromises()
    await wrapper.get('#email').setValue('user@allowed.com')
    await wrapper.get('#password').setValue('secret-123')
    await wrapper.get('#confirmPassword').setValue('secret-123')
    await wrapper.get('form').trigger('submit.prevent')
    await flushPromises()

    expect(registerMock).toHaveBeenCalledWith(
      expect.objectContaining({ email: 'user@allowed.com' })
    )
    expect(showErrorMock).not.toHaveBeenCalled()
  })

  it('shows LOGIN_IP_BANNED instead of invalid invitation code when validateInvitationCode fails with 403', async () => {
    vi.useFakeTimers()
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      turnstile_enabled: false,
      invitation_code_enabled: true
    })
    validateInvitationCodeMock.mockRejectedValue({
      status: 403,
      reason: 'LOGIN_IP_BANNED',
      message: 'This IP has been permanently banned after 3 failed login attempts.'
    })

    const wrapper = mountRegister()
    await flushPromises()
    await wrapper.get('#email').setValue('user@example.com')
    await wrapper.get('#password').setValue('secret-123')
    await wrapper.get('#confirmPassword').setValue('secret-123')
    await wrapper.get('#invitation_code').setValue('valid-looking-code')

    await vi.advanceTimersByTimeAsync(500)
    await flushPromises()

    expect(showErrorMock).toHaveBeenCalledWith('auth.errors.LOGIN_IP_BANNED')
    expect(showErrorMock).not.toHaveBeenCalledWith('auth.invitationCodeInvalid')
    expect(wrapper.get('#invitation_code').classes()).not.toContain('border-red-500')

    showErrorMock.mockReset()
    await wrapper.get('form').trigger('submit.prevent')
    await flushPromises()

    expect(showErrorMock).toHaveBeenCalledWith('auth.errors.LOGIN_IP_BANNED')
    expect(showErrorMock).not.toHaveBeenCalledWith('auth.invitationCodeInvalidCannotRegister')
    expect(registerMock).not.toHaveBeenCalled()
    vi.useRealTimers()
  })

  it('shows RATE_LIMITED when validateInvitationCode returns HTTP 429', async () => {
    vi.useFakeTimers()
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      turnstile_enabled: false,
      invitation_code_enabled: true
    })
    validateInvitationCodeMock.mockRejectedValueOnce({
      status: 429,
      message: 'Too many requests, please try again later'
    })

    const wrapper = mountRegister()
    await flushPromises()
    await wrapper.get('#invitation_code').setValue('some-code')

    await vi.advanceTimersByTimeAsync(500)
    await flushPromises()

    expect(showErrorMock).toHaveBeenCalledWith('auth.errors.RATE_LIMITED')
    expect(showErrorMock).not.toHaveBeenCalledWith('auth.invitationCodeInvalid')
    vi.useRealTimers()
  })

  it('shows LOGIN_PROTECTION_UNAVAILABLE when validateInvitationCode returns HTTP 503', async () => {
    vi.useFakeTimers()
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      turnstile_enabled: false,
      invitation_code_enabled: true
    })
    validateInvitationCodeMock.mockRejectedValueOnce({
      status: 503,
      reason: 'LOGIN_PROTECTION_UNAVAILABLE',
      message: 'Login protection is temporarily unavailable.'
    })

    const wrapper = mountRegister()
    await flushPromises()
    await wrapper.get('#invitation_code').setValue('some-code')

    await vi.advanceTimersByTimeAsync(500)
    await flushPromises()

    expect(showErrorMock).toHaveBeenCalledWith('auth.errors.LOGIN_PROTECTION_UNAVAILABLE')
    vi.useRealTimers()
  })

  it('still reports invalid invitation code when backend returns valid: false with INVITATION_CODE_NOT_FOUND', async () => {
    vi.useFakeTimers()
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      turnstile_enabled: false,
      invitation_code_enabled: true
    })
    validateInvitationCodeMock.mockResolvedValueOnce({
      valid: false,
      error_code: 'INVITATION_CODE_NOT_FOUND'
    })

    const wrapper = mountRegister()
    await flushPromises()
    await wrapper.get('#invitation_code').setValue('missing-code')

    await vi.advanceTimersByTimeAsync(500)
    await flushPromises()

    expect(showErrorMock).toHaveBeenCalledWith('auth.invitationCodeInvalid')
    expect(wrapper.get('#invitation_code').classes()).toContain('border-red-500')
    vi.useRealTimers()
  })

  it('shows LOGIN_IP_BANNED instead of invalid promo code when validatePromoCode fails with 403', async () => {
    vi.useFakeTimers()
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      turnstile_enabled: false,
      promo_code_enabled: true
    })
    validatePromoCodeMock.mockRejectedValueOnce({
      status: 403,
      reason: 'LOGIN_IP_BANNED',
      message: 'This IP has been permanently banned after 3 failed login attempts.'
    })

    const wrapper = mountRegister()
    await flushPromises()
    await wrapper.get('#promo_code').setValue('PROMO123')

    await vi.advanceTimersByTimeAsync(500)
    await flushPromises()

    expect(showErrorMock).toHaveBeenCalledWith('auth.errors.LOGIN_IP_BANNED')
    expect(showErrorMock).not.toHaveBeenCalledWith('auth.promoCodeInvalid')
    expect(wrapper.get('#promo_code').classes()).not.toContain('border-red-500')
    vi.useRealTimers()
  })
})
