import QtQuick
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Commons as Commons
import qs.Ui

// Remote control: AC state in the bar, controls in a popup.
// All device access goes through the rc daemon. "rc watch" streams every
// state change as one JSON line; buttons run "rc <device> <action>".
Panel {
    id: remote
    moduleName: "kidush.remote-control"
    ipcTarget: "kidush.remote-control"
    manageIpc: false

    Commons.Translations { id: strings; directory: Qt.resolvedUrl("i18n") }

    readonly property string home: Quickshell.env("HOME") || ""
    readonly property string rcPath: String(setting("binary", "") || "") || (home + "/.local/bin/rc")
    readonly property bool fake: setting("fake", false) === true
    readonly property var baseArgs: fake ? [rcPath, "--fake"] : [rcPath]

    property var devices: []
    property bool connected: false
    property string watchError: ""
    property string selectedId: ""
    readonly property var selected: devices.find(d => d.id === selectedId) || null
    readonly property var primary: devices.length > 0 ? devices[0] : null

    readonly property color fg: bar ? bar.foreground : Color.foreground
    readonly property color barFg: bar ? bar.barForeground : Color.foreground
    readonly property color urgent: bar ? bar.urgent : Color.urgent
    readonly property color dim: Qt.darker(fg, 1.4)
    readonly property string fontFamily: bar ? bar.fontFamily : Style.font.family

    readonly property var modeOptions: [
        { value: "cool", label: strings.t("mode.cool") },
        { value: "heat", label: strings.t("mode.heat") },
        { value: "dry", label: strings.t("mode.dry") },
        { value: "fan", label: strings.t("mode.fan") },
        { value: "auto", label: strings.t("mode.auto") }
    ]
    readonly property var fanOptions: [
        { value: "auto", label: strings.t("fan.auto") },
        { value: "low", label: strings.t("fan.low") },
        { value: "mid", label: strings.t("fan.mid") },
        { value: "high", label: strings.t("fan.high") }
    ]

    function label(options, value) {
        const o = options.find(x => x.value === value)
        return o ? o.label : value
    }

    function summary(d) {
        if (!d) return ""
        if (!d.state.power) return strings.t("device.off")
        return strings.t("device.on")
            .arg(label(modeOptions, d.state.mode))
            .arg(d.state.temp)
            .arg(label(fanOptions, d.state.fan).toLowerCase())
    }

    // The one line of trouble to show, if any, for a device or the bar.
    function problem(d) {
        if (!connected) return watchError !== "" ? watchError : strings.t("error.daemon")
        if (!d) return ""
        if (d.setup) return strings.t("error.setup").arg(d.setup)
        if (d.error) return strings.t("error.failed").arg(d.error)
        return ""
    }

    function load(line) {
        let msg
        try {
            msg = JSON.parse(line)
        } catch (e) {
            return
        }
        if (!msg || !Array.isArray(msg.devices)) return
        devices = msg.devices
        connected = true
        watchError = ""
    }

    function send(id, args) {
        Quickshell.execDetached(baseArgs.concat([id]).concat(args))
    }

    // Settings change in place; reconnect so the stream follows them. The
    // exit handler schedules the restart.
    onBaseArgsChanged: {
        devices = []
        connected = false
        if (watcher.running) watcher.running = false
        else restart.restart()
    }

    implicitWidth: button.implicitWidth
    implicitHeight: button.implicitHeight

    IpcHandler {
        target: "kidush.remote-control"

        function open(): void { remote.open() }
        function close(): void { remote.close() }
        function show(): void { remote.open() }
        function hide(): void { remote.close() }
        function toggle(): void { remote.toggle() }
        // Opens straight into one device's controls, e.g. from a keybinding.
        function openDevice(id: string): void {
            remote.selectedId = id
            remote.open()
        }
        function state(): string {
            return JSON.stringify({ connected: remote.connected, fake: remote.fake, rc: remote.rcPath, settings: remote.settings, error: remote.watchError, devices: remote.devices })
        }
    }

    Process {
        id: watcher
        command: remote.baseArgs.concat(["watch"])
        running: true
        stdout: SplitParser {
            onRead: function(line) { remote.load(line) }
        }
        stderr: StdioCollector {
            id: watchStderr
            onStreamFinished: {
                const msg = text.trim().replace(/^rc: /, "")
                if (msg !== "") remote.watchError = msg
            }
        }
        onExited: function(code) {
            remote.connected = false
            if (code === 127 || code === 255) remote.watchError = strings.t("error.binary").arg(remote.rcPath)
            restart.start()
        }
    }

    // The daemon may start after the shell, or restart; keep reconnecting.
    Timer {
        id: restart
        interval: 3000
        onTriggered: watcher.running = true
    }

    // ---------- Bar button ----------
    WidgetButton {
        id: button
        anchors.fill: parent
        bar: remote.bar
        hasVisualContent: true
        labelVisible: false
        fixedWidth: vertical ? -1 : content.implicitWidth + scaledHorizontalMargin * 2
        fixedHeight: vertical ? content.implicitHeight + scaledVerticalPadding * 2 : -1
        tooltipText: ""
        onPressed: function(b) { remote.toggle() }

        Row {
            id: content
            anchors.centerIn: parent
            spacing: Style.space(4)
            readonly property bool on: remote.primary !== null && remote.primary.state.power
            readonly property bool trouble: remote.problem(remote.primary) !== ""

            Text {
                anchors.verticalCenter: parent.verticalCenter
                textFormat: Text.PlainText
                text: content.trouble ? "󰀦" : "󰜗"
                color: content.trouble ? remote.urgent : remote.barFg
                opacity: content.on || content.trouble ? 1 : 0.45
                font.family: remote.fontFamily
                font.pixelSize: Style.bar.iconFont + 2
            }
            Text {
                visible: content.on && !button.vertical
                anchors.verticalCenter: parent.verticalCenter
                textFormat: Text.PlainText
                text: content.on ? remote.primary.state.temp + "°" : ""
                color: remote.barFg
                font.family: remote.fontFamily
                font.pixelSize: Style.font.body
            }
        }
    }

    // ---------- Popup ----------
    KeyboardPanel {
        id: popup
        anchorItem: button
        owner: remote
        bar: remote.bar
        open: remote.opened
        focusTarget: keys
        contentWidth: popup.fittedContentWidth(Style.space(380))
        contentHeight: popup.fittedContentHeight(column.implicitHeight)

        PanelKeyCatcher {
            id: keys
            anchors.fill: parent
            onCloseRequested: remote.close()
            onTabRequested: function(direction) { remote.switchPanel(direction) }

            Column {
                id: column
                anchors.left: parent.left
                anchors.right: parent.right
                anchors.top: parent.top
                spacing: Style.space(10)

                // ----- Device list -----
                PanelSectionHeader {
                    visible: remote.selected === null
                    text: strings.t("devices.header")
                    foreground: remote.fg
                    fontFamily: remote.fontFamily
                }
                Repeater {
                    model: remote.selected === null ? remote.devices : []

                    Button {
                        required property var modelData
                        width: column.width
                        leftAlign: true
                        iconText: "󰜗"
                        text: modelData.name + "  ·  " + remote.summary(modelData)
                        foreground: remote.fg
                        fontFamily: remote.fontFamily
                        onClicked: remote.selectedId = modelData.id
                    }
                }

                // ----- Device detail -----
                Item {
                    visible: remote.selected !== null
                    width: parent.width
                    implicitHeight: hero.implicitHeight

                    PanelActionButton {
                        id: back
                        anchors.left: parent.left
                        anchors.verticalCenter: parent.verticalCenter
                        iconText: "󰁍"
                        tooltipText: strings.t("device.back")
                        foreground: remote.fg
                        fontFamily: remote.fontFamily
                        onClicked: remote.selectedId = ""
                    }

                    PanelHero {
                        id: hero
                        anchors.left: back.visible ? back.right : parent.left
                        anchors.leftMargin: back.visible ? Style.space(8) : 0
                        anchors.right: parent.right
                        width: undefined
                        title: remote.selected ? remote.selected.name : ""
                        meta: remote.selected && remote.selected.pending ? strings.t("device.sending") : remote.summary(remote.selected)
                        foreground: remote.fg
                        fontFamily: remote.fontFamily
                        iconOpacity: remote.selected && remote.selected.state.power ? 1.0 : 0.5
                        iconComponent: Component {
                            Text {
                                textFormat: Text.PlainText
                                text: "󰜗"
                                color: remote.fg
                                font.family: remote.fontFamily
                                font.pixelSize: Style.font.display
                            }
                        }
                        trailingControl: Component {
                            ToggleSwitch {
                                id: powerSwitch
                                enabled: remote.connected && remote.selected !== null && !remote.selected.setup
                                checked: remote.selected !== null && remote.selected.state.power
                                busy: remote.selected !== null && remote.selected.pending
                                foreground: remote.fg
                                onToggled: remote.send(remote.selected.id, ["toggle"])

                                PanelToolTip {
                                    visible: powerSwitch.containsMouse
                                    text: powerSwitch.checked ? strings.t("power.off") : strings.t("power.on")
                                    fontFamily: remote.fontFamily
                                }
                            }
                        }
                    }
                }

                Text {
                    visible: remote.problem(remote.selected) !== "" && (remote.selected !== null || !remote.connected)
                    width: parent.width
                    textFormat: Text.PlainText
                    text: remote.problem(remote.selected)
                    color: remote.urgent
                    font.family: remote.fontFamily
                    font.pixelSize: Style.font.bodySmall
                    wrapMode: Text.WordWrap
                }

                Column {
                    id: controls
                    visible: remote.selected !== null
                    enabled: remote.connected && remote.selected !== null && !remote.selected.setup
                    opacity: enabled ? 1 : 0.5
                    width: parent.width
                    spacing: Style.space(10)
                    readonly property var dev: remote.selected
                    readonly property var st: dev ? dev.state : ({ power: false, mode: "cool", temp: 24, fan: "auto", swing: false })

                    PanelSeparator { foreground: remote.fg }
                    PanelSectionHeader {
                        text: strings.t("mode.header")
                        foreground: remote.fg
                        fontFamily: remote.fontFamily
                    }
                    ButtonGroup {
                        focusable: false
                        options: remote.modeOptions
                        value: controls.st.mode
                        foreground: remote.fg
                        fontFamily: remote.fontFamily
                        onChanged: function(v) { remote.send(controls.dev.id, ["mode", v]) }
                    }

                    PanelSectionHeader {
                        text: strings.t("temp.header")
                        foreground: remote.fg
                        fontFamily: remote.fontFamily
                    }
                    Row {
                        spacing: Style.space(16)

                        Button {
                            anchors.verticalCenter: parent.verticalCenter
                            bordered: true
                            iconText: "󰍴"
                            tooltipText: strings.t("temp.down")
                            enabled: controls.dev !== null && controls.st.temp > controls.dev.limits.min_temp
                            foreground: remote.fg
                            fontFamily: remote.fontFamily
                            onClicked: remote.send(controls.dev.id, ["temp", "down"])
                        }
                        Text {
                            anchors.verticalCenter: parent.verticalCenter
                            textFormat: Text.PlainText
                            text: controls.st.temp + "°C"
                            color: remote.fg
                            font.family: remote.fontFamily
                            font.pixelSize: Style.font.title
                            font.bold: true
                        }
                        Button {
                            anchors.verticalCenter: parent.verticalCenter
                            bordered: true
                            iconText: "󰐕"
                            tooltipText: strings.t("temp.up")
                            enabled: controls.dev !== null && controls.st.temp < controls.dev.limits.max_temp
                            foreground: remote.fg
                            fontFamily: remote.fontFamily
                            onClicked: remote.send(controls.dev.id, ["temp", "up"])
                        }
                    }

                    PanelSectionHeader {
                        text: strings.t("fan.header")
                        foreground: remote.fg
                        fontFamily: remote.fontFamily
                    }
                    ButtonGroup {
                        focusable: false
                        options: remote.fanOptions
                        value: controls.st.fan
                        foreground: remote.fg
                        fontFamily: remote.fontFamily
                        onChanged: function(v) { remote.send(controls.dev.id, ["fan", v]) }
                    }

                    Toggle {
                        visible: controls.dev !== null && controls.dev.capabilities.swing
                        width: parent.width
                        label: strings.t("swing.label")
                        description: strings.t("swing.description")
                        checked: controls.st.swing
                        foreground: remote.fg
                        fontFamily: remote.fontFamily
                        onClicked: remote.send(controls.dev.id, ["swing", "toggle"])
                    }

                    PanelSeparator {
                        visible: presetFlow.visible
                        foreground: remote.fg
                    }
                    PanelSectionHeader {
                        visible: presetFlow.visible
                        text: strings.t("presets.header")
                        foreground: remote.fg
                        fontFamily: remote.fontFamily
                    }
                    Flow {
                        id: presetFlow
                        visible: controls.dev !== null && controls.dev.presets.length > 0
                        width: parent.width
                        spacing: Style.space(8)

                        Repeater {
                            model: controls.dev ? controls.dev.presets : []

                            Button {
                                required property var modelData
                                bordered: true
                                text: modelData.name
                                tooltipText: remote.label(remote.modeOptions, modelData.mode) + " " + modelData.temp + "°C · " + remote.label(remote.fanOptions, modelData.fan)
                                foreground: remote.fg
                                fontFamily: remote.fontFamily
                                onClicked: remote.send(controls.dev.id, ["preset", modelData.name])
                            }
                        }
                    }

                    PanelSeparator { foreground: remote.fg }
                    Row {
                        spacing: Style.space(8)

                        Button {
                            iconText: "󰓦"
                            text: strings.t("actions.resend")
                            tooltipText: strings.t("actions.resendTip")
                            foreground: remote.fg
                            fontFamily: remote.fontFamily
                            fontSize: Style.font.bodySmall
                            onClicked: remote.send(controls.dev.id, ["resend"])
                        }
                        Button {
                            visible: controls.dev !== null && controls.dev.capabilities.fetch
                            iconText: "󰅢"
                            text: strings.t("actions.fetch")
                            tooltipText: strings.t("actions.fetchTip")
                            foreground: remote.fg
                            fontFamily: remote.fontFamily
                            fontSize: Style.font.bodySmall
                            onClicked: remote.send(controls.dev.id, ["fetch"])
                        }
                    }
                }
            }
        }
    }
}
