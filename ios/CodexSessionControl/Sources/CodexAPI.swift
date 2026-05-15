import Foundation

struct CodexThread: Decodable, Hashable, Identifiable {
    let id: String
    let title: String
    let cwd: String
    let status: String
    let modelProvider: String?
    let model: String?
    let updatedAt: String?
    let lastEventAt: String?
    let lastSignalKind: String?
    let lastSignal: String?
    let lastUser: String?
    let lastAssistant: String?
    let lastFinal: String?
    let tokensUsed: Int?
    let active: Bool

    enum CodingKeys: String, CodingKey {
        case id
        case title
        case cwd
        case status
        case modelProvider = "model_provider"
        case model
        case updatedAt = "updated_at"
        case lastEventAt = "last_event_at"
        case lastSignalKind = "last_signal_kind"
        case lastSignal = "last_signal"
        case lastUser = "last_user"
        case lastAssistant = "last_assistant"
        case lastFinal = "last_final"
        case tokensUsed = "tokens_used"
        case active
    }
}

struct ThreadsResponse: Decodable {
    let threads: [CodexThread]
}

struct CodexProject: Decodable, Hashable, Identifiable {
    var id: String { path }

    let name: String
    let path: String
    let relativePath: String

    enum CodingKeys: String, CodingKey {
        case name
        case path
        case relativePath = "relative_path"
    }
}

struct ProjectsResponse: Decodable {
    let root: String
    let projects: [CodexProject]
}

struct CodexGitBranch: Decodable, Hashable, Identifiable {
    var id: String { name }

    let name: String
    let current: Bool?
    let remote: Bool?
    let checkedOut: Bool?
    let worktreePath: String?

    enum CodingKeys: String, CodingKey {
        case name
        case current
        case remote
        case checkedOut = "checked_out"
        case worktreePath = "worktree_path"
    }
}

struct CodexGitWorktree: Decodable, Hashable, Identifiable {
    var id: String { path }

    let path: String
    let relativePath: String?
    let branch: String?
    let head: String?
    let current: Bool?

    enum CodingKeys: String, CodingKey {
        case path
        case relativePath = "relative_path"
        case branch
        case head
        case current
    }
}

struct ProjectGitContext: Decodable, Hashable {
    let cwd: String
    let repoPath: String?
    let isGit: Bool
    let currentBranch: String?
    let branches: [CodexGitBranch]?
    let worktrees: [CodexGitWorktree]?
    let suggestedWorktreeName: String?

    enum CodingKeys: String, CodingKey {
        case cwd
        case repoPath = "repo_path"
        case isGit = "is_git"
        case currentBranch = "current_branch"
        case branches
        case worktrees
        case suggestedWorktreeName = "suggested_worktree_name"
    }
}

struct WorktreeResponse: Decodable {
    let worktree: CodexGitWorktree
    let context: ProjectGitContext
}

struct CodexEvent: Decodable, Hashable, Identifiable {
    var id: String {
        eventID ?? "\(timestamp ?? "")-\(kind)-\(text.hashValue)"
    }

    let eventID: String?
    let timestamp: String?
    let kind: String
    let text: String
    let toolName: String?
    let failed: Bool?
    let escalation: Bool?
    let truncated: Bool?

    var isTruncated: Bool {
        truncated == true
    }

    enum CodingKeys: String, CodingKey {
        case eventID = "id"
        case timestamp
        case kind
        case text
        case toolName = "tool_name"
        case failed
        case escalation
        case truncated
    }
}

struct EventsResponse: Decodable {
    let events: [CodexEvent]
}

struct ContinueResponse: Decodable {
    let status: String
    let id: String
    let thread: CodexThread?
}

private struct ContinueRequest: Encodable {
    let prompt: String
    let model: String?
    let reasoningEffort: String?

    enum CodingKeys: String, CodingKey {
        case prompt
        case model
        case reasoningEffort = "reasoning_effort"
    }
}

private struct NewThreadRequest: Encodable {
    let cwd: String
    let prompt: String
    let model: String?
    let reasoningEffort: String?

    enum CodingKeys: String, CodingKey {
        case cwd
        case prompt
        case model
        case reasoningEffort = "reasoning_effort"
    }
}

private struct CreateWorktreeRequest: Encodable {
    let cwd: String
    let branch: String
    let name: String
}

struct CodexApproval: Decodable, Hashable, Identifiable {
    let id: String
    let kind: String
    let method: String
    let threadID: String?
    let turnID: String?
    let itemID: String?
    let approvalID: String?
    let command: String?
    let cwd: String?
    let reason: String?
    let grantRoot: String?
    let fileChanges: [String]?
    let createdAt: String?
    let expiresAt: String?

    enum CodingKeys: String, CodingKey {
        case id
        case kind
        case method
        case threadID = "thread_id"
        case turnID = "turn_id"
        case itemID = "item_id"
        case approvalID = "approval_id"
        case command
        case cwd
        case reason
        case grantRoot = "grant_root"
        case fileChanges = "file_changes"
        case createdAt = "created_at"
        case expiresAt = "expires_at"
    }
}

struct CodexApprovalsResponse: Decodable {
    let enabled: Bool?
    let codexExecApprovalsEnabled: Bool?
    let approvals: [CodexApproval]

    var isEnabled: Bool {
        enabled ?? codexExecApprovalsEnabled ?? false
    }

    enum CodingKeys: String, CodingKey {
        case enabled
        case codexExecApprovalsEnabled = "codex_exec_approvals_enabled"
        case approvals
    }
}

struct CodexApprovalSettingsResponse: Decodable {
    let enabled: Bool?
    let codexExecApprovalsEnabled: Bool?

    var isEnabled: Bool {
        enabled ?? codexExecApprovalsEnabled ?? false
    }

    enum CodingKeys: String, CodingKey {
        case enabled
        case codexExecApprovalsEnabled = "codex_exec_approvals_enabled"
    }
}

struct CodexApprovalDecisionResponse: Decodable {
    let status: String
    let id: String
    let decision: String
}

private struct CodexApprovalSettingsRequest: Encodable {
    let enabled: Bool
    let codexExecApprovalsEnabled: Bool

    enum CodingKeys: String, CodingKey {
        case enabled
        case codexExecApprovalsEnabled = "codex_exec_approvals_enabled"
    }
}

private struct CodexApprovalDecisionRequest: Encodable {
    let decision: String
}

struct ZukoApproval: Decodable, Hashable, Identifiable {
    let id: String
    let tool: String
    let args: [String]
    let scope: String
    let command: String
    let digest: String?
    let cwd: String?
    let pid: Int?
    let ppid: Int?
    let createdAt: String?
    let expiresAt: String?

    enum CodingKeys: String, CodingKey {
        case id
        case tool
        case args
        case scope
        case command
        case digest
        case cwd
        case pid
        case ppid
        case createdAt = "created_at"
        case expiresAt = "expires_at"
    }
}

struct ZukoApprovalsResponse: Decodable {
    let approvals: [ZukoApproval]
}

struct ZukoDecisionResponse: Decodable {
    let decision: String
}

private struct ZukoDecisionRequest: Encodable {
    let decision: String
}

private struct APIErrorResponse: Decodable {
    let error: String
}

enum CodexAPIError: LocalizedError {
    case invalidURL
    case transportSecurity
    case timedOut
    case server(String)
    case badStatus(Int)

    var errorDescription: String? {
        switch self {
        case .invalidURL:
            return "Invalid bridge URL"
        case .transportSecurity:
            return "iOS blocked this HTTP bridge URL. Reinstall the latest app build, or use an HTTPS bridge URL."
        case .timedOut:
            return "Bridge request timed out. Check that the Mac bridge is still running and reachable over Tailscale."
        case .server(let message):
            return message
        case .badStatus(let status):
            return "Bridge returned HTTP \(status)"
        }
    }
}

struct CodexAPI {
    let baseURL: URL

    init(baseURLString: String) throws {
        var text = baseURLString.trimmingCharacters(in: .whitespacesAndNewlines)
        if !text.localizedCaseInsensitiveContains("://") {
            text = "http://\(text)"
        }
        guard let url = URL(string: text), url.scheme != nil, url.host != nil else {
            throw CodexAPIError.invalidURL
        }
        baseURL = url
    }

    func threads(limit: Int = 80) async throws -> [CodexThread] {
        var components = URLComponents(url: baseURL.appendingPathComponent("api/threads"), resolvingAgainstBaseURL: false)
        components?.queryItems = [URLQueryItem(name: "limit", value: "\(limit)")]
        guard let url = components?.url else {
            throw CodexAPIError.invalidURL
        }

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        let response: ThreadsResponse = try await perform(request, timeout: 8)
        return response.threads
    }

    func projects() async throws -> [CodexProject] {
        let url = baseURL.appendingPathComponent("api/projects")

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        let response: ProjectsResponse = try await perform(request, timeout: 8)
        return response.projects
    }

    func projectGitContext(cwd: String) async throws -> ProjectGitContext {
        var components = URLComponents(url: baseURL.appendingPathComponent("api/project-git"), resolvingAgainstBaseURL: false)
        components?.queryItems = [URLQueryItem(name: "cwd", value: cwd)]
        guard let url = components?.url else {
            throw CodexAPIError.invalidURL
        }

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        return try await perform(request, timeout: 8)
    }

    func createWorktree(cwd: String, branch: String, name: String) async throws -> WorktreeResponse {
        let url = baseURL.appendingPathComponent("api/worktrees")

        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONEncoder().encode(CreateWorktreeRequest(
            cwd: cwd,
            branch: branch,
            name: name
        ))
        return try await perform(request, timeout: 30)
    }

    func events(threadID: String, limit: Int = 120) async throws -> [CodexEvent] {
        var components = URLComponents(
            url: baseURL
                .appendingPathComponent("api/threads")
                .appendingPathComponent(threadID)
                .appendingPathComponent("events"),
            resolvingAgainstBaseURL: false
        )
        components?.queryItems = [URLQueryItem(name: "limit", value: "\(limit)")]
        guard let url = components?.url else {
            throw CodexAPIError.invalidURL
        }

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        let response: EventsResponse = try await perform(request, timeout: 8)
        return response.events
    }

    func event(threadID: String, eventID: String) async throws -> CodexEvent {
        let url = baseURL
            .appendingPathComponent("api/threads")
            .appendingPathComponent(threadID)
            .appendingPathComponent("events")
            .appendingPathComponent(eventID)

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        return try await perform(request, timeout: 12)
    }

    func continueThread(
        id: String,
        prompt: String = "",
        model: String? = nil,
        reasoningEffort: String? = nil
    ) async throws -> ContinueResponse {
        let url = baseURL
            .appendingPathComponent("api/threads")
            .appendingPathComponent(id)
            .appendingPathComponent("continue")

        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONEncoder().encode(ContinueRequest(
            prompt: prompt,
            model: model,
            reasoningEffort: reasoningEffort
        ))
        return try await perform(request, timeout: 30)
    }

    func startThread(
        cwd: String,
        prompt: String = "",
        model: String? = nil,
        reasoningEffort: String? = nil
    ) async throws -> ContinueResponse {
        let url = baseURL.appendingPathComponent("api/new-thread")

        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONEncoder().encode(NewThreadRequest(
            cwd: cwd,
            prompt: prompt,
            model: model,
            reasoningEffort: reasoningEffort
        ))
        return try await perform(request, timeout: 30)
    }

    func codexApprovalSettings() async throws -> CodexApprovalSettingsResponse {
        let url = baseURL.appendingPathComponent("api/approvals/settings")

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        return try await perform(request, timeout: 8)
    }

    func updateCodexApprovalSettings(enabled: Bool) async throws -> CodexApprovalSettingsResponse {
        let url = baseURL.appendingPathComponent("api/approvals/settings")

        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONEncoder().encode(CodexApprovalSettingsRequest(
            enabled: enabled,
            codexExecApprovalsEnabled: enabled
        ))
        return try await perform(request, timeout: 8)
    }

    func codexApprovals() async throws -> CodexApprovalsResponse {
        let url = baseURL.appendingPathComponent("api/approvals")

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        return try await perform(request, timeout: 8)
    }

    func decideCodexApproval(id: String, decision: String) async throws -> CodexApprovalDecisionResponse {
        let url = baseURL
            .appendingPathComponent("api/approvals")
            .appendingPathComponent(id)
            .appendingPathComponent("decision")

        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONEncoder().encode(CodexApprovalDecisionRequest(decision: decision))
        return try await perform(request, timeout: 10)
    }

    private func perform<T: Decodable>(_ request: URLRequest, timeout: TimeInterval) async throws -> T {
        var request = request
        request.timeoutInterval = timeout
        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await URLSession.shared.data(for: request)
        } catch let error as URLError where error.code == .appTransportSecurityRequiresSecureConnection {
            throw CodexAPIError.transportSecurity
        } catch let error as URLError where error.code == .timedOut {
            throw CodexAPIError.timedOut
        }
        guard let httpResponse = response as? HTTPURLResponse else {
            throw CodexAPIError.badStatus(-1)
        }
        guard (200..<300).contains(httpResponse.statusCode) else {
            if let error = try? JSONDecoder().decode(APIErrorResponse.self, from: data) {
                throw CodexAPIError.server(error.error)
            }
            throw CodexAPIError.badStatus(httpResponse.statusCode)
        }
        return try JSONDecoder().decode(T.self, from: data)
    }
}

struct ZukoAPI {
    let baseURL: URL
    let token: String

    init(baseURLString: String, token: String) throws {
        var text = baseURLString.trimmingCharacters(in: .whitespacesAndNewlines)
        if !text.localizedCaseInsensitiveContains("://") {
            text = "http://\(text)"
        }
        guard let url = URL(string: text), url.scheme != nil, url.host != nil else {
            throw CodexAPIError.invalidURL
        }
        baseURL = url
        self.token = token.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    func approvals() async throws -> [ZukoApproval] {
        let url = baseURL.appendingPathComponent("v1/approvals")

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        let response: ZukoApprovalsResponse = try await perform(request, timeout: 8)
        return response.approvals
    }

    func decide(approvalID: String, decision: String) async throws -> ZukoDecisionResponse {
        let url = baseURL
            .appendingPathComponent("v1/approvals")
            .appendingPathComponent(approvalID)
            .appendingPathComponent("decision")

        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONEncoder().encode(ZukoDecisionRequest(decision: decision))
        return try await perform(request, timeout: 10)
    }

    private func perform<T: Decodable>(_ request: URLRequest, timeout: TimeInterval) async throws -> T {
        guard !token.isEmpty else {
            throw CodexAPIError.server("Missing zuko token")
        }

        var request = request
        request.timeoutInterval = timeout
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")

        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await URLSession.shared.data(for: request)
        } catch let error as URLError where error.code == .appTransportSecurityRequiresSecureConnection {
            throw CodexAPIError.transportSecurity
        } catch let error as URLError where error.code == .timedOut {
            throw CodexAPIError.timedOut
        }
        guard let httpResponse = response as? HTTPURLResponse else {
            throw CodexAPIError.badStatus(-1)
        }
        guard (200..<300).contains(httpResponse.statusCode) else {
            if let error = try? JSONDecoder().decode(APIErrorResponse.self, from: data) {
                throw CodexAPIError.server(error.error)
            }
            throw CodexAPIError.badStatus(httpResponse.statusCode)
        }
        return try JSONDecoder().decode(T.self, from: data)
    }
}

enum DateParser {
    private static let fractionalFormatter: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter
    }()

    private static let standardFormatter: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime]
        return formatter
    }()

    static func parse(_ value: String?) -> Date? {
        guard let value, !value.isEmpty else {
            return nil
        }
        return fractionalFormatter.date(from: value) ?? standardFormatter.date(from: value)
    }
}
