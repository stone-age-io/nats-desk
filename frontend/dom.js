// ============================================================================
// DOM REFERENCES
// ============================================================================
// One lookup table for every element the app touches.
// Modals are NOT here - dialogs.js builds those on demand (see dialogs.js).

const $ = (id) => document.getElementById(id);

export const els = {
  // --- Chrome ---------------------------------------------------------------
  toastContainer: $("toast-container"),
  tabBar: $("tabBar"),

  // --- Connection (app bar + popover) --------------------------------------
  connPopover: $("connPopover"),
  btnConnStatus: $("btnConnStatus"),
  statusText: $("statusText"),
  statusDot: $("statusDot"),
  statusHost: $("statusHost"),
  rttLabel: $("rttLabel"),
  btnInfo: $("btnInfo"),
  connListView: $("connListView"),
  connFormView: $("connFormView"),
  connFilter: $("connFilter"),
  connList: $("connList"),
  btnConnNew: $("btnConnNew"),
  btnConnDisconnect: $("btnConnDisconnect"),
  btnConnBack: $("btnConnBack"),

  // --- App settings (app bar + popover) -------------------------------------
  btnSettings: $("btnSettings"),
  settingsPopover: $("settingsPopover"),
  setVersion: $("setVersion"),
  setAutostart: $("setAutostart"),
  setAutostartRow: $("setAutostartRow"),
  setAutostartHint: $("setAutostartHint"),
  setQuitOnClose: $("setQuitOnClose"),
  setExe: $("setExe"),
  setLog: $("setLog"),
  setLogRow: $("setLogRow"),

  url: $("serverUrl"),
  creds: $("credsFile"),
  authUser: $("authUser"),
  authPass: $("authPass"),
  authToken: $("authToken"),
  btnConnect: $("btnConnect"),
  credsHint: $("credsHint"),

  profileField: $("profileField"),
  profileSelect: $("profileSelect"),
  btnSaveContext: $("btnSaveContext"),
  btnProfileDelete: $("btnProfileDelete"),

  // --- Monitor ---------------------------------------------------------------
  srcDataDot: $("srcDataDot"),
  srcSysDot: $("srcSysDot"),
  srcHttpDot: $("srcHttpDot"),

  monSysContext: $("monSysContext"),
  monSysManual: $("monSysManual"),
  monSysUrl: $("monSysUrl"),
  monSysUser: $("monSysUser"),
  monSysPass: $("monSysPass"),
  monSysCreds: $("monSysCreds"),
  monSysHint: $("monSysHint"),
  btnMonSysConnect: $("btnMonSysConnect"),
  btnMonSysDisconnect: $("btnMonSysDisconnect"),

  monHttpBases: $("monHttpBases"),
  monHttpInsecure: $("monHttpInsecure"),
  monHttpHint: $("monHttpHint"),
  btnMonHttpSave: $("btnMonHttpSave"),
  btnMonHttpClear: $("btnMonHttpClear"),

  monServerList: $("monServerList"),
  monServerCount: $("monServerCount"),
  btnMonRefresh: $("btnMonRefresh"),

  monEndpoint: $("monEndpoint"),
  monEndpointScope: $("monEndpointScope"),
  monEndpointVia: $("monEndpointVia"),
  monEndpointMeta: $("monEndpointMeta"),
  btnMonEndpoint: $("btnMonEndpoint"),
  monDetail: $("monDetail"),

  monEvents: $("monEvents"),
  monEventCount: $("monEventCount"),
  monEventFilter: $("monEventFilter"),
  btnMonEventsClear: $("btnMonEventsClear"),

  monAccount: $("monAccount"),
  btnMonAccountLoad: $("btnMonAccountLoad"),

  contextSelect: $("contextSelect"),
  contextHint: $("contextHint"),
  btnContextNew: $("btnContextNew"),
  btnContextEdit: $("btnContextEdit"),
  btnContextDelete: $("btnContextDelete"),
  btnContextDefault: $("btnContextDefault"),

  // --- Subscriptions --------------------------------------------------------
  subSubject: $("subSubject"),
  subQueue: $("subQueue"),
  btnSub: $("btnSub"),
  excludeSystemChk: $("excludeSystemChk"),
  excludeSystemRow: $("excludeSystemRow"),
  subList: $("subList"),
  subCount: $("subCount"),

  // --- Messaging: composer --------------------------------------------------
  composer: $("composer"),
  btnComposerToggle: $("btnComposerToggle"),
  templateSelect: $("templateSelect"),
  btnTemplateSave: $("btnTemplateSave"),
  btnTemplateDelete: $("btnTemplateDelete"),
  pubSubject: $("pubSubject"),
  pubReply: $("pubReply"),
  pubPayload: $("pubPayload"),
  btnHeaderToggle: $("btnHeaderToggle"),
  headerContainer: $("headerContainer"),
  headerRows: $("headerRows"),
  headerCount: $("headerCount"),
  btnHeaderAdd: $("btnHeaderAdd"),
  btnFormatPayload: $("btnFormatPayload"),
  reqTimeout: $("reqTimeout"),
  reqReplies: $("reqReplies"),
  btnPub: $("btnPub"),
  btnReq: $("btnReq"),

  // --- Messaging: log -------------------------------------------------------
  messages: $("messages"),
  logFilter: $("logFilter"),
  btnPause: $("btnPause"),
  btnClear: $("btnClear"),
  btnDownloadLogs: $("btnDownloadLogs"),
  btnLogOrder: $("btnLogOrder"),
  btnJumpLatest: $("btnJumpLatest"),
  jumpArrow: $("jumpArrow"),
  jumpCount: $("jumpCount"),

  // --- KV: buckets ----------------------------------------------------------
  kvBucketList: $("kvBucketList"),
  kvBucketFilter: $("kvBucketFilter"),
  kvBucketLabel: $("kvBucketLabel"),
  btnKvCreate: $("btnKvCreate"),
  btnKvRefresh: $("btnKvRefresh"),
  btnKvEdit: $("btnKvEdit"),
  btnKvDeleteBucket: $("btnKvDeleteBucket"),

  // --- KV: keys -------------------------------------------------------------
  kvKeyList: $("kvKeyList"),
  kvFilter: $("kvFilter"),

  // --- KV: detail -----------------------------------------------------------
  kvEmptyState: $("kvEmptyState"),
  kvDetailView: $("kvDetailView"),
  kvKeyInput: $("kvKeyInput"),
  btnKvGet: $("btnKvGet"),
  kvValueInput: $("kvValueInput"),
  kvValueHighlighter: $("kvValueHighlighter"),
  btnKvToggleMode: $("btnKvToggleMode"),
  btnKvCopy: $("btnKvCopy"),
  btnKvFormat: $("btnKvFormat"),
  kvRevLabel: $("kvRevLabel"),
  kvHistoryList: $("kvHistoryList"),
  kvHistoryCount: $("kvHistoryCount"),
  kvStatus: $("kvStatus"),
  btnKvPut: $("btnKvPut"),
  btnKvDelete: $("btnKvDelete"),
  btnKvPurge: $("btnKvPurge"),

  // --- Streams: list --------------------------------------------------------
  streamList: $("streamList"),
  streamFilter: $("streamFilter"),
  btnStreamCreate: $("btnStreamCreate"),
  btnStreamRefresh: $("btnStreamRefresh"),

  // --- Streams: detail ------------------------------------------------------
  streamEmptyState: $("streamEmptyState"),
  streamDetailView: $("streamDetailView"),
  streamNameTitle: $("streamNameTitle"),
  streamCreated: $("streamCreated"),
  btnStreamEdit: $("btnStreamEdit"),
  btnStreamPurge: $("btnStreamPurge"),
  btnStreamDelete: $("btnStreamDelete"),

  // --- Streams: overview ----------------------------------------------------
  streamSubjects: $("streamSubjects"),
  streamStorage: $("streamStorage"),
  streamRetention: $("streamRetention"),
  streamMsgs: $("streamMsgs"),
  streamBytes: $("streamBytes"),
  streamFirstSeq: $("streamFirstSeq"),
  streamLastSeq: $("streamLastSeq"),
  streamConsumerCount: $("streamConsumerCount"),

  // --- Streams: consumers ---------------------------------------------------
  consumerList: $("consumerList"),
  consumerTabCount: $("consumerTabCount"),
  btnConsumerCreate: $("btnConsumerCreate"),
  btnLoadConsumers: $("btnLoadConsumers"),

  // --- Services ---------------------------------------------------------------
  svcCount: $("svcCount"),
  btnSvcRefresh: $("btnSvcRefresh"),
  svcCensus: $("svcCensus"),
  svcFilter: $("svcFilter"),
  svcList: $("svcList"),
  svcInstancesLabel: $("svcInstancesLabel"),
  svcInstances: $("svcInstances"),

  svcEmptyState: $("svcEmptyState"),
  svcDetailView: $("svcDetailView"),
  svcTitle: $("svcTitle"),
  svcSubtitle: $("svcSubtitle"),
  svcDescription: $("svcDescription"),
  svcMeta: $("svcMeta"),
  btnSvcStats: $("btnSvcStats"),
  svcEndpointCount: $("svcEndpointCount"),
  svcEndpoints: $("svcEndpoints"),
  svcRaw: $("svcRaw"),

  svcReqSubject: $("svcReqSubject"),
  svcReqHint: $("svcReqHint"),
  svcReqPayload: $("svcReqPayload"),
  svcReqTimeout: $("svcReqTimeout"),
  btnSvcSend: $("btnSvcSend"),
  btnSvcFormat: $("btnSvcFormat"),
  btnSvcToComposer: $("btnSvcToComposer"),
  svcReply: $("svcReply"),

  // --- Streams: messages ----------------------------------------------------
  msgStartSeq: $("msgStartSeq"),
  msgEndSeq: $("msgEndSeq"),
  msgSubjectFilter: $("msgSubjectFilter"),
  btnStreamViewMsgs: $("btnStreamViewMsgs"),
  btnStreamTail: $("btnStreamTail"),
  btnStreamClearMsgs: $("btnStreamClearMsgs"),
  streamMsgFilter: $("streamMsgFilter"),
  streamMsgContainer: $("streamMsgContainer"),
};
