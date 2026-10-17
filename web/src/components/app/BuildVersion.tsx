export default function BuildVersion() {
  return <span className="build-version" title={__APP_REVISION__ ? `构建提交：${__APP_REVISION__}` : undefined}>
    版本 {__APP_VERSION__}{__APP_REVISION__ && <> · {__APP_REVISION__.slice(0, 7)}</>}
  </span>
}
