import { expect, it } from 'vitest'
import { credentialStatus, mcpConnectionInfo } from './credentials'
import type { MCPCredential } from './credentials'

it('formats generic connection details with the credential only in the authorization header', () => {
  for (const endpoint of ['https://hub.example.test/mcp', 'http://192.0.2.220:8080/mcp']) {
    const token = 'unit-test-token-not-a-real-secret'
    const info = mcpConnectionInfo(endpoint, token)
    expect(info.split('\n')).toEqual([
      'Transport: Streamable HTTP',
      `URL: ${endpoint}`,
      `Authorization: Bearer ${token}`,
    ])
    expect(info).not.toMatch(/OpenCode|oauth|apiKey=/i)
    expect(info.split('\n')[1]).not.toContain(token)
  }
})

it('rejects unsafe endpoints and ambiguous bearer values before copying connection details', () => {
  for (const endpoint of [
    '', 'javascript:alert(1)', '//evil.example',
    'https://user:pass@example.com/mcp', 'https://example.com/mcp?apiKey=secret',
    'https://example.com/mcp?token=secret', 'https://example.com/#token',
    'https://example.com/\\evil', 'https://example.com/\n',
  ]) {
    expect(() => mcpConnectionInfo(endpoint, 'fixture-token')).toThrow()
  }
  for (const token of ['', ' ', '\t', 'two tokens', 'token\nInjected: header', 'token\r', 'token\0', 'token\u007f']) {
    expect(() => mcpConnectionInfo('https://hub.example.test/mcp', token)).toThrow()
  }
})

it('distinguishes expiry, revocation and unknown server time', () => {
  const credential: MCPCredential = {
    id: 'fixture', organizationId: 'org-1', label: 'Client', clusterIds: ['east'], readOnly: true,
    createdAt: '2026-01-01T00:00:00Z', expiresAt: '2026-01-01T08:00:00Z', refreshUntil: '2026-01-31T00:00:00Z',
  }
  expect(credentialStatus(credential, Date.parse(credential.createdAt))).toBe('active')
  expect(credentialStatus(credential, Date.parse(credential.expiresAt))).toBe('expired')
  expect(credentialStatus({ ...credential, revokedAt: credential.createdAt }, 0)).toBe('revoked')
  expect(credentialStatus({ ...credential, expiresAt: 'invalid' }, 0)).toBe('unknown')
  expect(credentialStatus(credential, NaN)).toBe('unknown')
})
