import Foundation
import LocalAuthentication
import Security
import SwiftUI

private struct ZukoKeychainError: LocalizedError {
    let status: OSStatus

    var errorDescription: String? {
        "Keychain error \(status)"
    }
}

private enum ZukoTokenVault {
    private static let service = "CodexSessionControl.Zuko"
    private static let account = "approvalToken"

    static func load() -> String {
        var query = baseQuery()
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne

        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        guard status == errSecSuccess, let data = item as? Data else {
            return ""
        }
        return String(data: data, encoding: .utf8) ?? ""
    }

    static func save(_ token: String) throws {
        try delete()
        let trimmed = token.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, let data = trimmed.data(using: .utf8) else {
            return
        }

        var query = baseQuery()
        query[kSecValueData as String] = data
        let status = SecItemAdd(query as CFDictionary, nil)
        guard status == errSecSuccess else {
            throw ZukoKeychainError(status: status)
        }
    }

    static func delete() throws {
        let status = SecItemDelete(baseQuery() as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw ZukoKeychainError(status: status)
        }
    }

    private static func baseQuery() -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account
        ]
    }
}

private enum ApprovalLocalAuth {
    static func authenticate(reason: String) async throws {
        let context = LAContext()
        var error: NSError?
        let policy: LAPolicy = .deviceOwnerAuthentication
        guard context.canEvaluatePolicy(policy, error: &error) else {
            throw error ?? LAError(.biometryNotAvailable)
        }

        try await withCheckedThrowingContinuation { continuation in
            context.evaluatePolicy(policy, localizedReason: reason) { success, error in
                if success {
                    continuation.resume()
                } else {
                    continuation.resume(throwing: error ?? LAError(.authenticationFailed))
                }
            }
        }
    }
}

@MainActor
final class ZukoApprovalStore: ObservableObject {
    private static let zukoURLKey = "zukoServerURL"

    @Published var serverURLString: String {
        didSet {
            UserDefaults.standard.set(serverURLString, forKey: Self.zukoURLKey)
        }
    }
    @Published var token: String
    @Published var approvals: [ZukoApproval] = []
    @Published var isLoading = false
    @Published var decidingApprovalID: String?
    @Published var errorMessage: String?
    @Published var statusMessage: String?
    @Published var lastRefresh: Date?
    private var isPolling = false

    init() {
        serverURLString = UserDefaults.standard.string(forKey: Self.zukoURLKey) ?? ""
        token = ZukoTokenVault.load()
    }

    var isConfigured: Bool {
        !serverURLString.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !token.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    func saveToken(_ value: String) {
        do {
            let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
            try ZukoTokenVault.save(trimmed)
            token = trimmed
            statusMessage = "Zuko token saved"
            errorMessage = nil
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func poll(showSpinner: Bool = true) async {
        guard isConfigured else {
            approvals = []
            errorMessage = "Enter the zuko server URL and pair token."
            return
        }
        guard !isPolling else {
            return
        }

        isPolling = true
        if showSpinner {
            isLoading = true
        }
        defer {
            isPolling = false
            if showSpinner {
                isLoading = false
            }
        }

        do {
            let api = try ZukoAPI(baseURLString: serverURLString, token: token)
            approvals = try await api.approvals()
            lastRefresh = Date()
            errorMessage = nil
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func decide(_ approval: ZukoApproval, decision: String) async {
        guard decidingApprovalID == nil else {
            return
        }

        decidingApprovalID = approval.id
        errorMessage = nil
        statusMessage = nil
        defer { decidingApprovalID = nil }

        do {
            try await ApprovalLocalAuth.authenticate(reason: "Zuko: \(decision) \(approval.command)")
            let api = try ZukoAPI(baseURLString: serverURLString, token: token)
            _ = try await api.decide(approvalID: approval.id, decision: decision)
            approvals.removeAll { $0.id == approval.id }
            statusMessage = decision == "approve" ? "Approved \(approval.tool)" : "Denied \(approval.tool)"
            await poll(showSpinner: false)
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}

@MainActor
final class CodexApprovalStore: ObservableObject {
    @Published var enabled = true
    @Published var approvals: [CodexApproval] = []
    @Published var isLoading = false
    @Published var isSaving = false
    @Published var decidingApprovalID: String?
    @Published var errorMessage: String?
    @Published var statusMessage: String?
    @Published var lastRefresh: Date?
    private var isPolling = false

    func poll(bridgeURLString: String, showSpinner: Bool = true) async {
        guard !isPolling, !isSaving else {
            return
        }

        isPolling = true
        if showSpinner {
            isLoading = true
        }
        defer {
            isPolling = false
            if showSpinner {
                isLoading = false
            }
        }

        do {
            let api = try CodexAPI(baseURLString: bridgeURLString)
            let response = try await api.codexApprovals()
            enabled = response.isEnabled
            approvals = response.isEnabled ? response.approvals : []
            lastRefresh = Date()
            errorMessage = nil
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func setEnabled(bridgeURLString: String, enabled nextEnabled: Bool) async {
        guard !isSaving else {
            return
        }

        let previousEnabled = enabled
        enabled = nextEnabled
        if !nextEnabled {
            approvals = []
        }
        isSaving = true
        errorMessage = nil
        statusMessage = nil
        defer { isSaving = false }

        do {
            let api = try CodexAPI(baseURLString: bridgeURLString)
            let response = try await api.updateCodexApprovalSettings(enabled: nextEnabled)
            enabled = response.isEnabled
            if !response.isEnabled {
                approvals = []
            }
            statusMessage = response.isEnabled ? "Codex approvals enabled" : "Codex approvals disabled"
        } catch {
            enabled = previousEnabled
            errorMessage = error.localizedDescription
        }
    }

    func decide(_ approval: CodexApproval, decision: String, bridgeURLString: String) async {
        guard decidingApprovalID == nil else {
            return
        }

        decidingApprovalID = approval.id
        errorMessage = nil
        statusMessage = nil
        defer { decidingApprovalID = nil }

        do {
            try await ApprovalLocalAuth.authenticate(reason: "Codex: \(decision) \(approval.command ?? approval.kind)")
            let api = try CodexAPI(baseURLString: bridgeURLString)
            _ = try await api.decideCodexApproval(id: approval.id, decision: decision)
            approvals.removeAll { $0.id == approval.id }
            statusMessage = decision == "approve" ? "Approved Codex request" : "Denied Codex request"
            await poll(bridgeURLString: bridgeURLString, showSpinner: false)
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}

@MainActor
final class ThreadStore: ObservableObject {
    private static let bridgeURLKey = "bridgeURL"
    private static let selectedModelKey = "selectedModel"
    private static let selectedReasoningEffortKey = "selectedReasoningEffort"
    private static let defaultBridgeURL = "http://127.0.0.1:8765"

    @Published var threads: [CodexThread] = []
    @Published var projects: [CodexProject] = []
    @Published var eventsByThreadID: [String: [CodexEvent]] = [:]
    @Published var bridgeURLString: String {
        didSet {
            UserDefaults.standard.set(bridgeURLString, forKey: Self.bridgeURLKey)
        }
    }
    @Published var selectedModelID: String {
        didSet {
            UserDefaults.standard.set(selectedModelID, forKey: Self.selectedModelKey)
        }
    }
    @Published var selectedReasoningEffort: String {
        didSet {
            UserDefaults.standard.set(selectedReasoningEffort, forKey: Self.selectedReasoningEffortKey)
        }
    }
    @Published var isLoading = false
    @Published var isLoadingProjects = false
    @Published var isLoadingGitContext = false
    @Published var isCreatingWorktree = false
    @Published var isCreatingThread = false
    @Published var errorMessage: String?
    @Published var launchMessage: String?
    @Published var continuingThreadID: String?
    @Published var createdThreadID: String?
    @Published var lastRefresh: Date?
    private var isLoadingThreadList = false

    init() {
        bridgeURLString = UserDefaults.standard.string(forKey: Self.bridgeURLKey) ?? Self.defaultBridgeURL
        selectedModelID = UserDefaults.standard.string(forKey: Self.selectedModelKey) ?? ""
        selectedReasoningEffort = UserDefaults.standard.string(forKey: Self.selectedReasoningEffortKey) ?? ""
    }

    func load(showSpinner: Bool = true) async {
        guard !isLoadingThreadList else {
            return
        }
        isLoadingThreadList = true
        if showSpinner {
            isLoading = true
        }
        errorMessage = nil
        defer {
            isLoadingThreadList = false
            if showSpinner {
                isLoading = false
            }
        }

        do {
            let api = try CodexAPI(baseURLString: bridgeURLString)
            threads = try await api.threads()
            lastRefresh = Date()
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func thread(id: String) -> CodexThread? {
        threads.first { $0.id == id }
    }

    func events(for threadID: String) -> [CodexEvent] {
        eventsByThreadID[threadID] ?? []
    }

    func loadProjects() async {
        isLoadingProjects = true
        defer { isLoadingProjects = false }

        do {
            let api = try CodexAPI(baseURLString: bridgeURLString)
            projects = try await api.projects()
            errorMessage = nil
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func loadGitContext(project: CodexProject) async -> ProjectGitContext? {
        isLoadingGitContext = true
        defer { isLoadingGitContext = false }

        do {
            let api = try CodexAPI(baseURLString: bridgeURLString)
            let context = try await api.projectGitContext(cwd: project.path)
            launchMessage = nil
            return context
        } catch {
            launchMessage = error.localizedDescription
            return nil
        }
    }

    func createWorktree(project: CodexProject, branch: String, name: String) async -> WorktreeResponse? {
        isCreatingWorktree = true
        defer { isCreatingWorktree = false }

        do {
            let api = try CodexAPI(baseURLString: bridgeURLString)
            let response = try await api.createWorktree(cwd: project.path, branch: branch, name: name)
            launchMessage = nil
            return response
        } catch {
            launchMessage = error.localizedDescription
            return nil
        }
    }

    func refreshDetail(threadID: String) async {
        do {
            let api = try CodexAPI(baseURLString: bridgeURLString)
            async let nextThreads = api.threads()
            async let nextEvents = api.events(threadID: threadID)
            threads = try await nextThreads
            eventsByThreadID[threadID] = try await nextEvents
            lastRefresh = Date()
            errorMessage = nil
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func continueThread(_ thread: CodexThread, prompt: String) async -> Bool {
        continuingThreadID = thread.id
        defer { continuingThreadID = nil }

        do {
            let api = try CodexAPI(baseURLString: bridgeURLString)
            _ = try await api.continueThread(
                id: thread.id,
                prompt: prompt,
                model: selectedModelID.nilIfEmpty,
                reasoningEffort: selectedReasoningEffort.nilIfEmpty
            )
            launchMessage = nil
            await refreshDetail(threadID: thread.id)
            return true
        } catch {
            launchMessage = error.localizedDescription
            return false
        }
    }

    func startNewChat(cwd: String, prompt: String) async -> Bool {
        isCreatingThread = true
        defer { isCreatingThread = false }

        do {
            let api = try CodexAPI(baseURLString: bridgeURLString)
            let response = try await api.startThread(
                cwd: cwd,
                prompt: prompt,
                model: selectedModelID.nilIfEmpty,
                reasoningEffort: selectedReasoningEffort.nilIfEmpty
            )
            launchMessage = nil
            await load(showSpinner: false)
            if let thread = response.thread {
                upsertThread(thread)
            }
            if let id = response.id.nilIfEmpty {
                createdThreadID = id
            } else {
                launchMessage = response.status
            }
            return true
        } catch {
            launchMessage = error.localizedDescription
            return false
        }
    }

    private func upsertThread(_ thread: CodexThread) {
        threads.removeAll { $0.id == thread.id }
        threads.insert(thread, at: 0)
    }
}

private extension String {
    var nilIfEmpty: String? {
        let trimmed = trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? nil : trimmed
    }
}

struct ModelChoice: Identifiable, Hashable {
    let id: String
    let name: String
    let description: String
}

private let modelChoices: [ModelChoice] = [
    ModelChoice(id: "", name: "Current", description: "Use the thread/default model"),
    ModelChoice(id: "codex-auto-fast", name: "Auto Fast", description: "Fast auto mode"),
    ModelChoice(id: "codex-auto-balanced", name: "Auto Balanced", description: "Balanced auto mode"),
    ModelChoice(id: "codex-auto-thorough", name: "Auto Thorough", description: "Thorough auto mode"),
    ModelChoice(id: "gpt-5.3-codex", name: "GPT-5.3 Codex", description: "Codex flagship"),
    ModelChoice(id: "gpt-5.3-codex-spark", name: "GPT-5.3 Spark", description: "Fast coding model"),
    ModelChoice(id: "gpt-5.2-codex", name: "GPT-5.2 Codex", description: "Codex model"),
    ModelChoice(id: "gpt-5.2", name: "GPT-5.2", description: "General model"),
    ModelChoice(id: "gpt-5.1-codex-mini", name: "GPT-5.1 Mini", description: "Lightweight Codex model")
]

private let reasoningChoices: [ModelChoice] = [
    ModelChoice(id: "", name: "Default", description: "Use model default"),
    ModelChoice(id: "none", name: "None", description: "No reasoning"),
    ModelChoice(id: "minimal", name: "Minimal", description: "Fastest"),
    ModelChoice(id: "low", name: "Low", description: "Quick"),
    ModelChoice(id: "medium", name: "Medium", description: "Balanced"),
    ModelChoice(id: "high", name: "High", description: "Deeper"),
    ModelChoice(id: "xhigh", name: "XHigh", description: "Deepest")
]

struct ContentView: View {
    @StateObject private var store = ThreadStore()
    @StateObject private var zukoStore = ZukoApprovalStore()
    @StateObject private var codexApprovalStore = CodexApprovalStore()
    @State private var navigationPath = NavigationPath()
    @State private var searchText = ""
    @State private var showingSettings = false
    @State private var showingZuko = false
    @State private var showingNewChat = false

    private var filteredThreads: [CodexThread] {
        let query = searchText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !query.isEmpty else {
            return store.threads
        }
        return store.threads.filter { thread in
            thread.title.localizedCaseInsensitiveContains(query)
                || thread.cwd.localizedCaseInsensitiveContains(query)
                || thread.id.localizedCaseInsensitiveContains(query)
                || (thread.lastSignal?.localizedCaseInsensitiveContains(query) ?? false)
        }
    }

    private var hasPendingApprovals: Bool {
        !zukoStore.approvals.isEmpty || !codexApprovalStore.approvals.isEmpty
    }

    var body: some View {
        NavigationStack(path: $navigationPath) {
            Group {
                if store.isLoading && store.threads.isEmpty {
                    ProgressView()
                } else if let error = store.errorMessage, store.threads.isEmpty {
                    ContentUnavailableView(
                        "Bridge Unavailable",
                        systemImage: "network.slash",
                        description: Text(error)
                    )
                } else if filteredThreads.isEmpty {
                    ContentUnavailableView("No Threads", systemImage: "tray")
                } else {
                    List(filteredThreads) { thread in
                        NavigationLink(value: thread.id) {
                            ThreadRow(thread: thread)
                        }
                    }
                    .listStyle(.plain)
                    .refreshable {
                        await store.load(showSpinner: false)
                    }
                }
            }
            .navigationTitle("Codex")
            .searchable(text: $searchText, prompt: "Search")
            .toolbar {
                ToolbarItemGroup(placement: .topBarLeading) {
                    Button {
                        showingSettings = true
                    } label: {
                        Label("Settings", systemImage: "gearshape")
                    }
                    Button {
                        showingZuko = true
                    } label: {
                        Label("Approvals", systemImage: hasPendingApprovals ? "lock.shield.fill" : "lock.shield")
                    }
                }
                ToolbarItemGroup(placement: .topBarTrailing) {
                    Button {
                        showingNewChat = true
                    } label: {
                        Label("New Chat", systemImage: "square.and.pencil")
                    }
                    Button {
                        Task { await store.load() }
                    } label: {
                        Label("Refresh", systemImage: "arrow.clockwise")
                    }
                    .disabled(store.isLoading)
                }
            }
            .navigationDestination(for: String.self) { threadID in
                ThreadDetailView(
                    store: store,
                    zukoStore: zukoStore,
                    codexStore: codexApprovalStore,
                    threadID: threadID
                )
            }
            .sheet(isPresented: $showingSettings) {
                SettingsView(store: store)
            }
            .sheet(isPresented: $showingZuko) {
                ZukoApprovalsView(
                    store: zukoStore,
                    codexStore: codexApprovalStore,
                    bridgeURLString: store.bridgeURLString
                )
            }
            .sheet(isPresented: $showingNewChat) {
                NewChatView(store: store)
            }
            .alert("Codex", isPresented: launchAlertBinding) {
                Button("OK") {
                    store.launchMessage = nil
                }
            } message: {
                Text(store.launchMessage ?? "")
            }
            .task {
                await store.load()
            }
            .onChange(of: store.createdThreadID) { _, threadID in
                guard let threadID else {
                    return
                }
                showingNewChat = false
                navigationPath.append(threadID)
                store.createdThreadID = nil
            }
        }
    }

    private var launchAlertBinding: Binding<Bool> {
        Binding(
            get: { store.launchMessage != nil },
            set: { isPresented in
                if !isPresented {
                    store.launchMessage = nil
                }
            }
        )
    }
}

struct ThreadRow: View {
    let thread: CodexThread

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            StatusIndicator(status: thread.status)
                .padding(.top, 4)

            VStack(alignment: .leading, spacing: 7) {
                HStack(alignment: .firstTextBaseline) {
                    Text(thread.title.isEmpty ? "(untitled)" : thread.title)
                        .font(.headline)
                        .lineLimit(2)
                    Spacer(minLength: 8)
                    StatusBadge(status: thread.status)
                }

                Text(thread.cwd)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)

                if let signal = thread.lastSignal, !signal.isEmpty {
                    FinalAwarePreviewText(
                        text: signal,
                        isFinal: thread.lastSignalKind == "final"
                    )
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                }

                HStack(spacing: 12) {
                    if let updated = relativeTime(thread.updatedAt) {
                        Label(updated, systemImage: "clock")
                    }
                    if let model = thread.model, !model.isEmpty {
                        Label(model, systemImage: "cpu")
                    }
                }
                .font(.caption)
                .foregroundStyle(.tertiary)
                .labelStyle(.titleAndIcon)
            }
        }
        .padding(.vertical, 8)
    }
}

struct ThreadDetailView: View {
    @ObservedObject var store: ThreadStore
    @ObservedObject var zukoStore: ZukoApprovalStore
    @ObservedObject var codexStore: CodexApprovalStore
    let threadID: String
    @State private var prompt = ""

    private var thread: CodexThread? {
        store.thread(id: threadID)
    }

    private var events: [CodexEvent] {
        store.events(for: threadID)
    }

    private var newestEvents: [CodexEvent] {
        Array(events.suffix(40).reversed())
    }

    private var threadCodexApprovals: [CodexApproval] {
        guard let thread else {
            return []
        }
        return codexStore.approvals.filter { approval in
            approval.threadID == thread.id || approval.cwd?.isSameOrInside(thread.cwd) == true
        }
    }

    private var threadZukoApprovals: [ZukoApproval] {
        guard let thread else {
            return []
        }
        return zukoStore.approvals.filter { approval in
            approval.cwd?.isSameOrInside(thread.cwd) == true
        }
    }

    private var hasThreadApprovals: Bool {
        !threadCodexApprovals.isEmpty || !threadZukoApprovals.isEmpty
    }

    private var localCodexEscalation: String? {
        guard threadCodexApprovals.isEmpty,
              thread?.lastSignalKind == "escalation",
              let signal = thread?.lastSignal?.nilIfEmpty else {
            return nil
        }
        return signal
    }

    private var showsApprovalSection: Bool {
        hasThreadApprovals || localCodexEscalation != nil
    }

    var body: some View {
        Group {
            if let thread {
                ScrollView {
                    VStack(alignment: .leading, spacing: 22) {
                        VStack(alignment: .leading, spacing: 10) {
                            HStack {
                                StatusBadge(status: thread.status)
                                Spacer()
                                if let refreshed = store.lastRefresh {
                                    Text("Updated \(shortRelativeTime(refreshed))")
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                }
                            }
                            Text(thread.title.isEmpty ? "(untitled)" : thread.title)
                                .font(.title2.weight(.semibold))
                                .textSelection(.enabled)
                            Text(thread.cwd)
                                .font(.callout)
                                .foregroundStyle(.secondary)
                                .textSelection(.enabled)
                        }

                        SendPromptPanel(
                            store: store,
                            thread: thread,
                            prompt: $prompt,
                            isSending: store.continuingThreadID == thread.id
                        )

                        if showsApprovalSection {
                            DetailSection(title: "Approvals") {
                                ThreadApprovalsView(
                                    zukoStore: zukoStore,
                                    codexStore: codexStore,
                                    bridgeURLString: store.bridgeURLString,
                                    zukoApprovals: threadZukoApprovals,
                                    codexApprovals: threadCodexApprovals,
                                    localCodexEscalation: localCodexEscalation
                                )
                            }
                        }

                        DetailSection(title: "Latest") {
                            if let signal = thread.lastSignal, !signal.isEmpty {
                                if thread.lastSignalKind == "final" {
                                    MarkdownMessageView(text: signal)
                                } else {
                                    Text(signal)
                                        .textSelection(.enabled)
                                }
                            } else {
                                Text("No recent activity")
                                    .foregroundStyle(.secondary)
                            }
                        }

                        DetailSection(title: "Live Activity") {
                            if events.isEmpty {
                                Text("No events loaded")
                                    .foregroundStyle(.secondary)
                            } else {
                                VStack(alignment: .leading, spacing: 14) {
                                    ForEach(newestEvents) { event in
                                        EventRow(store: store, threadID: threadID, event: event)
                                    }
                                }
                            }
                        }

                        DetailSection(title: "Thread") {
                            DetailRow(label: "ID", value: thread.id)
                            if let updated = relativeTime(thread.updatedAt) {
                                DetailRow(label: "Updated", value: updated)
                            }
                            if let model = thread.model, !model.isEmpty {
                                DetailRow(label: "Model", value: model)
                            }
                            if let provider = thread.modelProvider, !provider.isEmpty {
                                DetailRow(label: "Provider", value: provider)
                            }
                        }
                    }
                    .padding()
                }
            } else {
                ContentUnavailableView("Thread Not Found", systemImage: "questionmark.folder")
            }
        }
        .navigationTitle("Thread")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button {
                    Task { await store.refreshDetail(threadID: threadID) }
                } label: {
                    Label("Refresh", systemImage: "arrow.clockwise")
                }
            }
        }
        .task(id: threadID) {
            await store.refreshDetail(threadID: threadID)
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(5))
                await store.refreshDetail(threadID: threadID)
            }
        }
        .task(id: store.bridgeURLString) {
            await codexStore.poll(bridgeURLString: store.bridgeURLString, showSpinner: false)
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(5))
                await codexStore.poll(bridgeURLString: store.bridgeURLString, showSpinner: false)
            }
        }
        .task(id: zukoStore.serverURLString + "|" + zukoStore.token) {
            guard zukoStore.isConfigured else {
                return
            }
            await zukoStore.poll(showSpinner: false)
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(5))
                await zukoStore.poll(showSpinner: false)
            }
        }
    }
}

private extension String {
    func isSameOrInside(_ root: String) -> Bool {
        let path = trimmingCharacters(in: .whitespacesAndNewlines)
        let root = root.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !path.isEmpty, !root.isEmpty else {
            return false
        }
        if path == root || path.hasPrefix(root + "/") {
            return true
        }
        if !path.hasPrefix("/") {
            return root == path || root.hasSuffix("/" + path)
        }
        if !root.hasPrefix("/") {
            return path == root || path.hasSuffix("/" + root)
        }
        return false
    }
}

struct ThreadApprovalsView: View {
    @ObservedObject var zukoStore: ZukoApprovalStore
    @ObservedObject var codexStore: CodexApprovalStore
    let bridgeURLString: String
    let zukoApprovals: [ZukoApproval]
    let codexApprovals: [CodexApproval]
    let localCodexEscalation: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            if let localCodexEscalation {
                LocalCodexEscalationRow(signal: localCodexEscalation)
            }

            ForEach(codexApprovals) { approval in
                CodexApprovalRow(
                    approval: approval,
                    isDeciding: codexStore.decidingApprovalID == approval.id,
                    approve: {
                        Task {
                            await codexStore.decide(
                                approval,
                                decision: "approve",
                                bridgeURLString: bridgeURLString
                            )
                        }
                    },
                    deny: {
                        Task {
                            await codexStore.decide(
                                approval,
                                decision: "deny",
                                bridgeURLString: bridgeURLString
                            )
                        }
                    }
                )
            }

            ForEach(zukoApprovals) { approval in
                ZukoApprovalRow(
                    approval: approval,
                    isDeciding: zukoStore.decidingApprovalID == approval.id,
                    approve: {
                        Task { await zukoStore.decide(approval, decision: "approve") }
                    },
                    deny: {
                        Task { await zukoStore.decide(approval, decision: "deny") }
                    }
                )
            }
        }
    }
}

struct SendPromptPanel: View {
    @ObservedObject var store: ThreadStore
    let thread: CodexThread
    @Binding var prompt: String
    let isSending: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Prompt")
                .font(.headline)

            TextEditor(text: $prompt)
                .frame(minHeight: 92)
                .padding(8)
                .background(.secondary.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
                .textInputAutocapitalization(.sentences)
                .autocorrectionDisabled()

            HStack {
                Picker("Model", selection: $store.selectedModelID) {
                    ForEach(modelChoices) { choice in
                        Text(choice.name).tag(choice.id)
                    }
                }
                .pickerStyle(.menu)

                Picker("Speed", selection: $store.selectedReasoningEffort) {
                    ForEach(reasoningChoices) { choice in
                        Text(choice.name).tag(choice.id)
                    }
                }
                .pickerStyle(.menu)
            }

            Button {
                let text = prompt.trimmingCharacters(in: .whitespacesAndNewlines)
                Task {
                    if await store.continueThread(thread, prompt: text) {
                        prompt = ""
                    }
                }
            } label: {
                Label(isSending ? "Sending" : "Send Prompt", systemImage: "paperplane.fill")
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
            .disabled(isSending || prompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
        }
    }
}

struct EventRow: View {
    @ObservedObject var store: ThreadStore
    let threadID: String
    let event: CodexEvent
    @State private var showingDetail = false
    @State private var detailEvent: CodexEvent?
    @State private var detailError: String?
    @State private var isLoadingDetail = false

    var body: some View {
        Group {
            if canShowDetail {
                Button {
                    showingDetail = true
                } label: {
                    rowContent
                }
                .buttonStyle(.plain)
            } else {
                rowContent
            }
        }
        .sheet(isPresented: $showingDetail) {
            EventDetailView(
                event: detailEvent ?? event,
                isLoading: isLoadingDetail,
                errorMessage: detailError
            )
            .task(id: event.id) {
                await loadDetail()
            }
        }
    }

    private var rowContent: some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack(spacing: 8) {
                Text(event.kind.uppercased())
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(eventColor)
                if let timestamp = eventTime {
                    Text(timestamp)
                        .font(.caption)
                        .foregroundStyle(.tertiary)
                }
                Spacer()
                if canShowDetail {
                    Image(systemName: "chevron.right")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.tertiary)
                }
            }
            FinalAwarePreviewText(
                text: event.text.isEmpty ? event.kind : event.text,
                isFinal: event.kind == "final"
            )
                .font(.callout)
                .foregroundStyle(event.failed == true ? .red : .primary)
                .lineLimit(event.isTruncated ? 1 : 3)
                .textSelection(.enabled)
        }
        .padding(.vertical, 2)
    }

    private var canShowDetail: Bool {
        event.isTruncated || event.prefersMarkdownRendering || event.text.needsDetailPreview
    }

    private func loadDetail() async {
        guard event.isTruncated, detailEvent == nil, let eventID = event.eventID else {
            return
        }
        isLoadingDetail = true
        detailError = nil
        defer { isLoadingDetail = false }

        do {
            let api = try CodexAPI(baseURLString: store.bridgeURLString)
            detailEvent = try await api.event(threadID: threadID, eventID: eventID)
        } catch {
            detailError = error.localizedDescription
        }
    }

    private var eventTime: String? {
        relativeTime(event.timestamp)
    }

    private var eventColor: Color {
        if event.failed == true || event.escalation == true {
            return .red
        }
        switch event.kind {
        case "user":
            return .blue
        case "assistant", "final":
            return .green
        case "tool", "tool-call":
            return .orange
        default:
            return .secondary
        }
    }
}

struct FinalAwarePreviewText: View {
    let text: String
    let isFinal: Bool

    var body: some View {
        if isFinal {
            MarkdownInlineText(text: text)
        } else {
            Text(text)
        }
    }
}

private extension String {
    var needsDetailPreview: Bool {
        let trimmed = trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            return false
        }
        if trimmed.count > 240 {
            return true
        }
        let lineCount = trimmed.split(whereSeparator: \.isNewline).count
        return lineCount > 3
    }
}

struct EventDetailView: View {
    let event: CodexEvent
    let isLoading: Bool
    let errorMessage: String?
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    HStack(spacing: 8) {
                        Text(event.kind.uppercased())
                            .font(.caption.weight(.semibold))
                            .foregroundStyle(event.failed == true || event.escalation == true ? .red : .secondary)
                        if let timestamp = relativeTime(event.timestamp) {
                            Text(timestamp)
                                .font(.caption)
                                .foregroundStyle(.tertiary)
                        }
                    }

                    if let toolName = event.toolName, !toolName.isEmpty {
                        DetailRow(label: "Tool", value: toolName)
                    }

                    if isLoading {
                        ProgressView()
                    } else if let errorMessage {
                        Text(errorMessage)
                            .foregroundStyle(.red)
                    } else if event.prefersMarkdownRendering {
                        MarkdownMessageView(text: event.text.isEmpty ? event.kind : event.text)
                    } else {
                        Text(event.text.isEmpty ? event.kind : event.text)
                            .font(.system(.body, design: .monospaced))
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
                .padding()
            }
            .navigationTitle("Event")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") {
                        dismiss()
                    }
                }
            }
        }
    }
}

private extension CodexEvent {
    var prefersMarkdownRendering: Bool {
        switch kind {
        case "assistant", "final":
            return true
        default:
            return false
        }
    }
}

struct MarkdownMessageView: View {
    let text: String

    private var blocks: [MarkdownBlock] {
        MarkdownBlock.parse(text)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            ForEach(Array(blocks.enumerated()), id: \.offset) { _, block in
                switch block {
                case .text(let text):
                    MarkdownTextBlock(text: text)
                case .code(let codeBlock):
                    MarkdownCodeBlockView(codeBlock: codeBlock)
                case .table(let table):
                    MarkdownTableView(table: table)
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .textSelection(.enabled)
    }
}

enum MarkdownBlock {
    case text(String)
    case code(MarkdownCodeBlock)
    case table(MarkdownTable)

    static func parse(_ text: String) -> [MarkdownBlock] {
        let lines = text.components(separatedBy: .newlines)
        var blocks: [MarkdownBlock] = []
        var textBuffer: [String] = []
        var index = 0

        func flushText() {
            let blockText = textBuffer.joined(separator: "\n").trimmingCharacters(in: .whitespacesAndNewlines)
            if !blockText.isEmpty {
                blocks.append(.text(blockText))
            }
            textBuffer.removeAll(keepingCapacity: true)
        }

        while index < lines.count {
            if let parsed = MarkdownCodeBlock.parse(lines: lines, startIndex: index) {
                flushText()
                blocks.append(.code(parsed.codeBlock))
                index = parsed.nextIndex
            } else if let parsed = MarkdownTable.parse(lines: lines, startIndex: index) {
                flushText()
                blocks.append(.table(parsed.table))
                index = parsed.nextIndex
            } else {
                textBuffer.append(lines[index])
                index += 1
            }
        }
        flushText()
        return blocks
    }
}

struct MarkdownCodeBlock {
    let language: String
    let code: String

    static func parse(lines: [String], startIndex: Int) -> (codeBlock: MarkdownCodeBlock, nextIndex: Int)? {
        guard startIndex < lines.count, let fence = MarkdownFence(line: lines[startIndex]) else {
            return nil
        }

        var codeLines: [String] = []
        var cursor = startIndex + 1
        while cursor < lines.count {
            if MarkdownFence(line: lines[cursor], closingFor: fence) != nil {
                return (
                    MarkdownCodeBlock(language: fence.language, code: codeLines.joined(separator: "\n")),
                    cursor + 1
                )
            }
            codeLines.append(lines[cursor])
            cursor += 1
        }

        return (
            MarkdownCodeBlock(language: fence.language, code: codeLines.joined(separator: "\n")),
            cursor
        )
    }
}

struct MarkdownFence {
    let marker: Character
    let count: Int
    let language: String

    init?(line: String, closingFor opening: MarkdownFence? = nil) {
        let trimmed = line.trimmingCharacters(in: .whitespaces)
        guard let first = trimmed.first, first == "`" || first == "~" else {
            return nil
        }

        var markerCount = 0
        for character in trimmed {
            if character == first {
                markerCount += 1
            } else {
                break
            }
        }
        guard markerCount >= 3 else {
            return nil
        }

        if let opening {
            guard first == opening.marker, markerCount >= opening.count else {
                return nil
            }
        }

        marker = first
        count = markerCount
        let languageStart = trimmed.index(trimmed.startIndex, offsetBy: markerCount)
        language = String(trimmed[languageStart...])
            .trimmingCharacters(in: .whitespacesAndNewlines)
    }
}

struct MarkdownTable {
    let headers: [String]
    let alignments: [MarkdownTableAlignment]
    let rows: [[String]]

    static func parse(lines: [String], startIndex: Int) -> (table: MarkdownTable, nextIndex: Int)? {
        guard startIndex + 1 < lines.count else {
            return nil
        }
        let headerLine = lines[startIndex]
        let separatorLine = lines[startIndex + 1]
        guard headerLine.contains("|"), separatorLine.contains("|") else {
            return nil
        }

        let headers = splitMarkdownTableRow(headerLine)
        let separatorCells = splitMarkdownTableRow(separatorLine)
        guard headers.count >= 2, separatorCells.count >= headers.count else {
            return nil
        }

        let alignments = separatorCells.prefix(headers.count).compactMap(MarkdownTableAlignment.init(separator:))
        guard alignments.count == headers.count else {
            return nil
        }

        var rows: [[String]] = []
        var cursor = startIndex + 2
        while cursor < lines.count {
            let line = lines[cursor]
            if line.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !line.contains("|") {
                break
            }
            let cells = splitMarkdownTableRow(line)
            guard cells.count >= 2 else {
                break
            }
            rows.append(normalizeMarkdownTableCells(cells, count: headers.count))
            cursor += 1
        }

        return (
            MarkdownTable(
                headers: normalizeMarkdownTableCells(headers, count: headers.count),
                alignments: alignments,
                rows: rows
            ),
            cursor
        )
    }
}

enum MarkdownTableAlignment {
    case leading
    case center
    case trailing

    init?(separator: String) {
        let trimmed = separator.trimmingCharacters(in: .whitespaces)
        guard trimmed.count >= 3 else {
            return nil
        }

        let leftAligned = trimmed.hasPrefix(":")
        let rightAligned = trimmed.hasSuffix(":")
        let marker = trimmed.trimmingCharacters(in: CharacterSet(charactersIn: ":"))
        guard marker.count >= 3, marker.allSatisfy({ $0 == "-" }) else {
            return nil
        }

        if leftAligned && rightAligned {
            self = .center
        } else if rightAligned {
            self = .trailing
        } else {
            self = .leading
        }
    }

    var frameAlignment: Alignment {
        switch self {
        case .leading:
            return .leading
        case .center:
            return .center
        case .trailing:
            return .trailing
        }
    }
}

struct MarkdownTextBlock: View {
    let text: String

    var body: some View {
        MarkdownInlineText(text: text)
            .frame(maxWidth: .infinity, alignment: .leading)
    }
}

struct MarkdownInlineText: View {
    let text: String

    var body: some View {
        if let attributed = try? AttributedString(markdown: text) {
            Text(attributed)
        } else {
            Text(text)
        }
    }
}

struct MarkdownCodeBlockView: View {
    let codeBlock: MarkdownCodeBlock

    var body: some View {
        ScrollView(.horizontal, showsIndicators: true) {
            VStack(alignment: .leading, spacing: 0) {
                if !codeBlock.language.isEmpty {
                    Text(codeBlock.language)
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.secondary)
                        .padding(.horizontal, 12)
                        .padding(.top, 10)
                        .padding(.bottom, 4)
                }

                Text(codeBlock.code.isEmpty ? " " : codeBlock.code)
                    .font(.system(.callout, design: .monospaced))
                    .foregroundStyle(.primary)
                    .lineSpacing(3)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: true, vertical: false)
                    .padding(.horizontal, 12)
                    .padding(.top, codeBlock.language.isEmpty ? 12 : 4)
                    .padding(.bottom, 12)
            }
            .background(.secondary.opacity(0.06), in: RoundedRectangle(cornerRadius: 8))
            .overlay {
                RoundedRectangle(cornerRadius: 8)
                    .stroke(.secondary.opacity(0.16), lineWidth: 1)
            }
        }
    }
}

struct MarkdownTableView: View {
    let table: MarkdownTable

    private var columnWidths: [CGFloat] {
        table.headers.indices.map { index in
            let lengths = [table.headers[index].count] + table.rows.map { row in
                index < row.count ? row[index].count : 0
            }
            let maxLength = lengths.max() ?? 0
            return min(max(CGFloat(maxLength) * 7 + 32, 96), 220)
        }
    }

    var body: some View {
        ScrollView(.horizontal, showsIndicators: true) {
            VStack(alignment: .leading, spacing: 0) {
                MarkdownTableRow(
                    cells: table.headers,
                    alignments: table.alignments,
                    columnWidths: columnWidths,
                    isHeader: true
                )
                Divider()
                ForEach(table.rows.indices, id: \.self) { index in
                    MarkdownTableRow(
                        cells: table.rows[index],
                        alignments: table.alignments,
                        columnWidths: columnWidths,
                        isHeader: false
                    )
                    if index != table.rows.count - 1 {
                        Divider()
                    }
                }
            }
            .background(.secondary.opacity(0.05), in: RoundedRectangle(cornerRadius: 8))
            .overlay {
                RoundedRectangle(cornerRadius: 8)
                    .stroke(.secondary.opacity(0.18), lineWidth: 1)
            }
        }
    }
}

struct MarkdownTableRow: View {
    let cells: [String]
    let alignments: [MarkdownTableAlignment]
    let columnWidths: [CGFloat]
    let isHeader: Bool

    var body: some View {
        HStack(alignment: .top, spacing: 0) {
            ForEach(columnWidths.indices, id: \.self) { index in
                MarkdownTableCell(
                    text: index < cells.count ? cells[index] : "",
                    alignment: index < alignments.count ? alignments[index].frameAlignment : .leading,
                    textAlignment: index < alignments.count ? alignments[index].textAlignment : .leading,
                    width: columnWidths[index],
                    isHeader: isHeader
                )
                if index != columnWidths.count - 1 {
                    Divider()
                }
            }
        }
    }
}

struct MarkdownTableCell: View {
    let text: String
    let alignment: Alignment
    let textAlignment: TextAlignment
    let width: CGFloat
    let isHeader: Bool

    var body: some View {
        MarkdownTextBlock(text: text)
            .font(isHeader ? .callout.weight(.semibold) : .callout)
            .foregroundStyle(isHeader ? .primary : .secondary)
            .multilineTextAlignment(textAlignment)
            .padding(.horizontal, 10)
            .padding(.vertical, 8)
            .frame(width: width, alignment: alignment)
    }
}

private extension MarkdownTableAlignment {
    var textAlignment: TextAlignment {
        switch self {
        case .trailing:
            return .trailing
        case .center:
            return .center
        case .leading:
            return .leading
        }
    }
}

private func splitMarkdownTableRow(_ line: String) -> [String] {
    var body = line.trimmingCharacters(in: .whitespaces)
    if body.hasPrefix("|") {
        body.removeFirst()
    }
    if body.hasSuffix("|") {
        body.removeLast()
    }

    var cells: [String] = []
    var current = ""
    var escaping = false

    for character in body {
        if escaping {
            if character != "|" {
                current.append("\\")
            }
            current.append(character)
            escaping = false
        } else if character == "\\" {
            escaping = true
        } else if character == "|" {
            cells.append(current.trimmingCharacters(in: .whitespaces))
            current.removeAll(keepingCapacity: true)
        } else {
            current.append(character)
        }
    }

    if escaping {
        current.append("\\")
    }
    cells.append(current.trimmingCharacters(in: .whitespaces))
    return cells
}

private func normalizeMarkdownTableCells(_ cells: [String], count: Int) -> [String] {
    if cells.count == count {
        return cells
    }
    if cells.count > count {
        return Array(cells.prefix(count))
    }
    return cells + Array(repeating: "", count: count - cells.count)
}

struct DetailSection<Content: View>: View {
    let title: String
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(title)
                .font(.headline)
            content
                .font(.body)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

struct DetailRow: View {
    let label: String
    let value: String

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            Text(label)
                .foregroundStyle(.secondary)
            Spacer(minLength: 16)
            Text(value)
                .multilineTextAlignment(.trailing)
                .textSelection(.enabled)
        }
    }
}

struct NewChatView: View {
    @ObservedObject var store: ThreadStore
    @Environment(\.dismiss) private var dismiss
    @State private var selectedProjectPath = ""
    @State private var selectedCheckoutPath = ""
    @State private var gitContext: ProjectGitContext?
    @State private var selectedBranch = ""
    @State private var worktreeName = ""
    @State private var prompt = ""
    @State private var searchText = ""

    private var filteredProjects: [CodexProject] {
        let query = searchText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !query.isEmpty else {
            return store.projects
        }
        return store.projects.filter { project in
            project.name.localizedCaseInsensitiveContains(query)
                || project.relativePath.localizedCaseInsensitiveContains(query)
                || project.path.localizedCaseInsensitiveContains(query)
        }
    }

    private var selectedProject: CodexProject? {
        store.projects.first { $0.path == selectedProjectPath }
    }

    private var selectedStartCWD: String? {
        if !selectedCheckoutPath.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return selectedCheckoutPath
        }
        return selectedProject?.path
    }

    private var worktrees: [CodexGitWorktree] {
        gitContext?.worktrees ?? []
    }

    private var otherWorktrees: [CodexGitWorktree] {
        worktrees.filter { worktree in
            worktree.path != selectedProject?.path
        }
    }

    private var branches: [CodexGitBranch] {
        gitContext?.branches ?? []
    }

    var body: some View {
        NavigationStack {
            List {
                Section("Prompt") {
                    TextEditor(text: $prompt)
                        .frame(minHeight: 110)
                        .textInputAutocapitalization(.sentences)
                        .autocorrectionDisabled()
                }

                Section("Model") {
                    Picker("Model", selection: $store.selectedModelID) {
                        ForEach(modelChoices) { choice in
                            Text(choice.name).tag(choice.id)
                        }
                    }
                    Picker("Speed", selection: $store.selectedReasoningEffort) {
                        ForEach(reasoningChoices) { choice in
                            Text(choice.name).tag(choice.id)
                        }
                    }
                }

                Section("Project") {
                    if store.isLoadingProjects && store.projects.isEmpty {
                        ProgressView()
                    } else if filteredProjects.isEmpty {
                        Text("No projects found")
                            .foregroundStyle(.secondary)
                    } else {
                        ForEach(filteredProjects) { project in
                            Button {
                                selectProject(project)
                            } label: {
                                ProjectChoiceRow(
                                    project: project,
                                    isSelected: selectedProjectPath == project.path
                                )
                            }
                            .buttonStyle(.plain)
                        }
                    }
                }

                Section("Checkout") {
                    if let selectedProject {
                        Button {
                            selectedCheckoutPath = selectedProject.path
                        } label: {
                            CheckoutChoiceRow(
                                title: "Selected folder",
                                subtitle: selectedProject.relativePath,
                                badge: gitContext?.currentBranch,
                                isSelected: selectedCheckoutPath == selectedProject.path
                            )
                        }
                        .buttonStyle(.plain)

                        if store.isLoadingGitContext {
                            ProgressView()
                        } else if gitContext?.isGit == true {
                            ForEach(otherWorktrees) { worktree in
                                Button {
                                    selectedCheckoutPath = worktree.path
                                } label: {
                                    CheckoutChoiceRow(
                                        title: worktreeTitle(worktree),
                                        subtitle: worktree.relativePath ?? worktree.path,
                                        badge: worktree.branch,
                                        isSelected: selectedCheckoutPath == worktree.path
                                    )
                                }
                                .buttonStyle(.plain)
                            }
                        }
                    } else {
                        Text("No project selected")
                            .foregroundStyle(.secondary)
                    }
                }

                if gitContext?.isGit == true {
                    Section("Worktree") {
                        Picker("Branch", selection: $selectedBranch) {
                            ForEach(branches) { branch in
                                Text(branchTitle(branch)).tag(branch.name)
                            }
                        }

                        TextField("Name", text: $worktreeName)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()

                        Button {
                            guard let selectedProject else {
                                return
                            }
                            let branch = selectedBranch
                            let name = worktreeName.trimmingCharacters(in: .whitespacesAndNewlines)
                            Task {
                                if let response = await store.createWorktree(project: selectedProject, branch: branch, name: name) {
                                    gitContext = response.context
                                    selectedCheckoutPath = response.worktree.path
                                    if let branch = response.worktree.branch, !branch.isEmpty {
                                        selectedBranch = branch
                                    }
                                    if let suggested = response.context.suggestedWorktreeName, !suggested.isEmpty {
                                        worktreeName = suggested
                                    }
                                }
                            }
                        } label: {
                            Label(store.isCreatingWorktree ? "Creating" : "Create Worktree", systemImage: "plus.rectangle.on.folder")
                        }
                        .disabled(selectedBranch.isEmpty || store.isCreatingWorktree)
                    }
                }
            }
            .navigationTitle("New Chat")
            .navigationBarTitleDisplayMode(.inline)
            .searchable(text: $searchText, prompt: "Find project")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") {
                        dismiss()
                    }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button(store.isCreatingThread ? "Starting" : "Start") {
                        guard let cwd = selectedStartCWD else {
                            return
                        }
                        let text = prompt.trimmingCharacters(in: .whitespacesAndNewlines)
                        Task {
                            if await store.startNewChat(cwd: cwd, prompt: text) {
                                prompt = ""
                                dismiss()
                            }
                        }
                    }
                    .disabled(selectedStartCWD == nil || store.isCreatingThread)
                }
            }
            .task {
                await store.loadProjects()
                if selectedProjectPath.isEmpty, let firstProject = store.projects.first {
                    selectProject(firstProject)
                }
            }
            .onChange(of: store.projects) { _, projects in
                if selectedProjectPath.isEmpty, let firstProject = projects.first {
                    selectProject(firstProject)
                }
            }
            .task(id: selectedProjectPath) {
                await loadSelectedProjectGitContext()
            }
        }
    }

    private func selectProject(_ project: CodexProject) {
        selectedProjectPath = project.path
        selectedCheckoutPath = project.path
        selectedBranch = ""
        worktreeName = ""
        gitContext = nil
    }

    private func loadSelectedProjectGitContext() async {
        guard let selectedProject else {
            return
        }
        guard let context = await store.loadGitContext(project: selectedProject) else {
            return
        }
        gitContext = context
        if selectedCheckoutPath.isEmpty {
            selectedCheckoutPath = selectedProject.path
        }
        if selectedBranch.isEmpty {
            selectedBranch = context.currentBranch ?? context.branches?.first?.name ?? ""
        }
        if worktreeName.isEmpty {
            worktreeName = context.suggestedWorktreeName ?? ""
        }
    }

    private func worktreeTitle(_ worktree: CodexGitWorktree) -> String {
        if worktree.current == true {
            return "Current worktree"
        }
        return URL(fileURLWithPath: worktree.path).lastPathComponent
    }

    private func branchTitle(_ branch: CodexGitBranch) -> String {
        var title = branch.name
        if branch.current == true {
            title += " current"
        } else if branch.checkedOut == true {
            title += " checked out"
        }
        return title
    }
}

struct ProjectChoiceRow: View {
    let project: CodexProject
    let isSelected: Bool

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: isSelected ? "checkmark.circle.fill" : "circle")
                .foregroundStyle(isSelected ? .blue : .secondary)
                .padding(.top, 2)

            VStack(alignment: .leading, spacing: 4) {
                Text(project.name)
                    .font(.body.weight(.medium))
                    .foregroundStyle(.primary)
                Text(project.relativePath)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
        }
        .padding(.vertical, 4)
    }
}

struct CheckoutChoiceRow: View {
    let title: String
    let subtitle: String
    let badge: String?
    let isSelected: Bool

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: isSelected ? "checkmark.circle.fill" : "circle")
                .foregroundStyle(isSelected ? .blue : .secondary)
                .padding(.top, 2)

            VStack(alignment: .leading, spacing: 4) {
                HStack(alignment: .firstTextBaseline) {
                    Text(title)
                        .font(.body.weight(.medium))
                    if let badge, !badge.isEmpty {
                        Text(badge)
                            .font(.caption.weight(.semibold))
                            .foregroundStyle(.secondary)
                    }
                }
                Text(subtitle)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
        }
        .padding(.vertical, 4)
    }
}

struct ZukoApprovalsView: View {
    @ObservedObject var store: ZukoApprovalStore
    @ObservedObject var codexStore: CodexApprovalStore
    @Environment(\.dismiss) private var dismiss
    let bridgeURLString: String
    @State private var draftURL: String
    @State private var draftToken: String

    init(store: ZukoApprovalStore, codexStore: CodexApprovalStore, bridgeURLString: String) {
        self.store = store
        self.codexStore = codexStore
        self.bridgeURLString = bridgeURLString
        _draftURL = State(initialValue: store.serverURLString)
        _draftToken = State(initialValue: store.token)
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("Zuko Connection") {
                    TextField("Server URL", text: $draftURL)
                        .keyboardType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()

                    SecureField("Pair token", text: $draftToken)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()

                    Button {
                        saveConnection()
                        Task { await store.poll() }
                    } label: {
                        Label("Save Connection", systemImage: "key")
                    }
                }

                Section("Codex Exec Approvals") {
                    Toggle(isOn: codexApprovalToggle) {
                        Label("Approve in app", systemImage: codexStore.enabled ? "checkmark.shield" : "shield")
                    }
                    .disabled(codexStore.isSaving)

                    if codexStore.isSaving {
                        ProgressView()
                    }
                }

                Section("Codex Requests") {
                    if codexStore.isLoading && codexStore.approvals.isEmpty {
                        ProgressView()
                    } else if !codexStore.enabled {
                        Label("Codex approvals off", systemImage: "power")
                            .foregroundStyle(.secondary)
                    } else if codexStore.approvals.isEmpty {
                        Label("No pending Codex requests", systemImage: "checkmark.shield")
                            .foregroundStyle(.secondary)
                    } else {
                        ForEach(codexStore.approvals) { approval in
                            CodexApprovalRow(
                                approval: approval,
                                isDeciding: codexStore.decidingApprovalID == approval.id,
                                approve: {
                                    Task {
                                        await codexStore.decide(
                                            approval,
                                            decision: "approve",
                                            bridgeURLString: bridgeURLString
                                        )
                                    }
                                },
                                deny: {
                                    Task {
                                        await codexStore.decide(
                                            approval,
                                            decision: "deny",
                                            bridgeURLString: bridgeURLString
                                        )
                                    }
                                }
                            )
                        }
                    }
                }

                Section("Zuko Requests") {
                    if store.isLoading && store.approvals.isEmpty {
                        ProgressView()
                    } else if !store.isConfigured {
                        Label("Enter the zuko server URL and pair token", systemImage: "lock.shield")
                            .foregroundStyle(.secondary)
                    } else if store.approvals.isEmpty {
                        Label("No pending approvals", systemImage: "checkmark.shield")
                            .foregroundStyle(.secondary)
                    } else {
                        ForEach(store.approvals) { approval in
                            ZukoApprovalRow(
                                approval: approval,
                                isDeciding: store.decidingApprovalID == approval.id,
                                approve: {
                                    Task { await store.decide(approval, decision: "approve") }
                                },
                                deny: {
                                    Task { await store.decide(approval, decision: "deny") }
                                }
                            )
                        }
                    }
                }

                if store.lastRefresh != nil || codexStore.lastRefresh != nil {
                    Section("Status") {
                        if let refreshed = codexStore.lastRefresh {
                            DetailRow(label: "Codex", value: shortRelativeTime(refreshed))
                        }
                        if let refreshed = store.lastRefresh {
                            DetailRow(label: "Zuko", value: shortRelativeTime(refreshed))
                        }
                    }
                }

                if let status = codexStore.statusMessage {
                    Section {
                        Text(status)
                            .foregroundStyle(.secondary)
                    }
                }

                if let status = store.statusMessage {
                    Section {
                        Text(status)
                            .foregroundStyle(.secondary)
                    }
                }

                if let error = codexStore.errorMessage {
                    Section {
                        Text(error)
                            .foregroundStyle(.red)
                    }
                }

                if let error = store.errorMessage {
                    Section {
                        Text(error)
                            .foregroundStyle(.red)
                    }
                }
            }
            .navigationTitle("Approvals")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Done") {
                        dismiss()
                    }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button {
                        Task { await refreshNow() }
                    } label: {
                        Label("Refresh", systemImage: "arrow.clockwise")
                    }
                }
            }
            .task(id: store.serverURLString + "|" + store.token) {
                guard store.isConfigured else {
                    return
                }
                await store.poll()
                while !Task.isCancelled {
                    try? await Task.sleep(for: .seconds(5))
                    await store.poll(showSpinner: false)
                }
            }
            .task(id: bridgeURLString) {
                await codexStore.poll(bridgeURLString: bridgeURLString)
                while !Task.isCancelled {
                    try? await Task.sleep(for: .seconds(5))
                    await codexStore.poll(bridgeURLString: bridgeURLString, showSpinner: false)
                }
            }
        }
    }

    private var codexApprovalToggle: Binding<Bool> {
        Binding(
            get: { codexStore.enabled },
            set: { enabled in
                Task {
                    await codexStore.setEnabled(bridgeURLString: bridgeURLString, enabled: enabled)
                }
            }
        )
    }

    private func saveConnection() {
        store.serverURLString = draftURL.trimmingCharacters(in: .whitespacesAndNewlines)
        store.saveToken(draftToken)
    }

    private func refreshNow() async {
        saveConnection()
        await store.poll()
        await codexStore.poll(bridgeURLString: bridgeURLString)
    }
}

private struct LocalCodexEscalationPayload: Decodable {
    let cmd: String?
    let command: String?
    let cwd: String?
    let workdir: String?
    let justification: String?
    let sandboxPermissions: String?

    enum CodingKeys: String, CodingKey {
        case cmd
        case command
        case cwd
        case workdir
        case justification
        case sandboxPermissions = "sandbox_permissions"
    }
}

struct LocalCodexEscalationRow: View {
    let signal: String

    private var payload: LocalCodexEscalationPayload? {
        guard let jsonStart = signal.firstIndex(of: "{") else {
            return nil
        }
        let jsonText = String(signal[jsonStart...])
        guard let data = jsonText.data(using: .utf8) else {
            return nil
        }
        return try? JSONDecoder().decode(LocalCodexEscalationPayload.self, from: data)
    }

    private var commandText: String {
        payload?.cmd?.nilIfEmpty
            ?? payload?.command?.nilIfEmpty
            ?? signal.replacingOccurrences(of: "ESCALATION REQUESTED ", with: "").nilIfEmpty
            ?? "Codex approval request"
    }

    private var cwd: String? {
        payload?.cwd?.nilIfEmpty ?? payload?.workdir?.nilIfEmpty
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .firstTextBaseline) {
                Text("Local Codex")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                Spacer(minLength: 8)
                Label("Mac", systemImage: "desktopcomputer")
                    .font(.caption)
                    .foregroundStyle(.tertiary)
            }

            Text(commandText)
                .font(.system(.body, design: .monospaced))
                .textSelection(.enabled)
                .lineLimit(4)

            if let reason = payload?.justification?.nilIfEmpty {
                Text(reason)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(3)
            }

            if let cwd {
                Text(cwd)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }

            Text("Approve this one on the Mac. Future turns started from this app route Codex approvals here.")
                .font(.caption)
                .foregroundStyle(.orange)
        }
        .padding(.vertical, 6)
    }
}

struct CodexApprovalRow: View {
    let approval: CodexApproval
    let isDeciding: Bool
    let approve: () -> Void
    let deny: () -> Void

    private var title: String {
        switch approval.kind {
        case "file_change":
            return "File Change"
        case "command":
            return "Command"
        default:
            return "Codex"
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .firstTextBaseline) {
                Text(title)
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                Spacer(minLength: 8)
                if let expiresAt = approval.expiresAt, let expires = DateParser.parse(expiresAt) {
                    Text("Expires \(shortRelativeTime(expires))")
                        .font(.caption)
                        .foregroundStyle(.tertiary)
                }
            }

            Text(approval.command?.nilIfEmpty ?? "Codex approval request")
                .font(.system(.body, design: .monospaced))
                .textSelection(.enabled)
                .lineLimit(4)

            if let reason = approval.reason?.nilIfEmpty {
                Text(reason)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(3)
            }

            if let cwd = approval.cwd?.nilIfEmpty {
                Text(cwd)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }

            if let fileChanges = approval.fileChanges, !fileChanges.isEmpty {
                Text(fileChanges.prefix(3).joined(separator: ", "))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }

            HStack {
                Button(role: .destructive) {
                    deny()
                } label: {
                    Label("Deny", systemImage: "xmark")
                }
                .buttonStyle(.bordered)

                Spacer()

                Button {
                    approve()
                } label: {
                    Label("Approve", systemImage: "checkmark")
                }
                .buttonStyle(.borderedProminent)
            }
            .disabled(isDeciding)

            if isDeciding {
                ProgressView()
            }
        }
        .padding(.vertical, 6)
    }
}

struct ZukoApprovalRow: View {
    let approval: ZukoApproval
    let isDeciding: Bool
    let approve: () -> Void
    let deny: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .firstTextBaseline) {
                Text(approval.scope)
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                Spacer(minLength: 8)
                if let expiresAt = approval.expiresAt, let expires = DateParser.parse(expiresAt) {
                    Text("Expires \(shortRelativeTime(expires))")
                        .font(.caption)
                        .foregroundStyle(.tertiary)
                }
            }

            Text(approval.command)
                .font(.system(.body, design: .monospaced))
                .textSelection(.enabled)
                .lineLimit(4)

            if let cwd = approval.cwd, !cwd.isEmpty {
                Text(cwd)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }

            HStack {
                Button(role: .destructive) {
                    deny()
                } label: {
                    Label("Deny", systemImage: "xmark")
                }
                .buttonStyle(.bordered)

                Spacer()

                Button {
                    approve()
                } label: {
                    Label("Approve", systemImage: "checkmark")
                }
                .buttonStyle(.borderedProminent)
            }
            .disabled(isDeciding)

            if isDeciding {
                ProgressView()
            }
        }
        .padding(.vertical, 6)
    }
}

struct SettingsView: View {
    @ObservedObject var store: ThreadStore
    @Environment(\.dismiss) private var dismiss
    @State private var draftURL: String

    init(store: ThreadStore) {
        self.store = store
        _draftURL = State(initialValue: store.bridgeURLString)
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("Bridge") {
                    TextField("URL", text: $draftURL)
                        .keyboardType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                }
            }
            .navigationTitle("Settings")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") {
                        dismiss()
                    }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") {
                        store.bridgeURLString = draftURL
                        Task { await store.load() }
                        dismiss()
                    }
                }
            }
        }
    }
}

struct StatusBadge: View {
    let status: String

    var body: some View {
        Text(status)
            .font(.caption.weight(.semibold))
            .padding(.horizontal, 8)
            .padding(.vertical, 4)
            .foregroundStyle(statusColor(status))
            .background(statusColor(status).opacity(0.12), in: Capsule())
    }
}

struct StatusIndicator: View {
    let status: String

    var body: some View {
        Circle()
            .fill(statusColor(status))
            .frame(width: 10, height: 10)
            .shadow(color: statusColor(status).opacity(status == "LIVE" ? 0.6 : 0), radius: 5)
    }
}

private func statusColor(_ status: String) -> Color {
    switch status {
    case "ALERT":
        return .red
    case "LIVE":
        return .green
    case "FINAL":
        return .blue
    default:
        return .secondary
    }
}

private func relativeTime(_ rawValue: String?) -> String? {
    guard let date = DateParser.parse(rawValue) else {
        return nil
    }
    return shortRelativeTime(date)
}

private func shortRelativeTime(_ date: Date) -> String {
    let formatter = RelativeDateTimeFormatter()
    formatter.unitsStyle = .short
    return formatter.localizedString(for: date, relativeTo: Date())
}
