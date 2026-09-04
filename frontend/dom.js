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

  // --- App settings (app bar + popover) -------------------------------------
  btnSettings: $("btnSettings"),
  settingsPopover: $("settingsPopover"),
  setVersion: $("setVersion"),
  setAutostart: $("setAutostart"),
  setAutostartRow: $("setAutostartRow"),
  setAutostartHint: $("setAutostartHint"),
  setExe: $("setExe"),
  setLog: $("setLog"),
  setLogRow: $("setLogRow"),

  url: $("serverUrl"),
  creds: $("credsFile"),
  authUser: $("authUser"),
  authPass: $("authPass"),
  authToken: $("authToken"),
  btnConnect: $("btnConnect"),
  saveCredsChk: $("saveCredsChk"),
  credsHint: $("credsHint"),

  profileSelect: $("profileSelect"),
  btnProfileSave: $("btnProfileSave"),
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
  monEndpointVia: $("monEndpointVia"),
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
  pubPayload: $("pubPayload"),
  btnHeaderToggle: $("btnHeaderToggle"),
  headerContainer: $("headerContainer"),
  headerRows: $("headerRows"),
  headerCount: $("headerCount"),
  btnHeaderAdd: $("btnHeaderAdd"),
  btnFormatPayload: $("btnFormatPayload"),
  reqTimeout: $("reqTimeout"),
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
