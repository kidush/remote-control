# Tuya cloud setup

The Positivo Casa Inteligente IR blaster (PCUIRN) is a Tuya device. `rc`
reaches it through the Tuya Cloud API, the same way the Positivo app and
Alexa do. This needs a free Tuya developer project linked to the app account
that owns the blaster. You do this once, in the browser, in about 20 minutes.

Tuya renames console menus from time to time. If a label below does not
match, look for the closest one; the steps stay the same.

## 1. Create the cloud project

1. Sign up or log in at <https://platform.tuya.com> (the IoT platform).
2. Open **Cloud > Development** and click **Create Cloud Project**.
3. Fill in:
   - **Project name**: anything, e.g. `remote-control`
   - **Industry**: Smart Home
   - **Development method**: Smart Home
   - **Data center**: **Western America** (Brazilian accounts live there).
     It must match the app account's region, or no devices will show up.
4. On the services step, keep the defaults and make sure
   **IR Control Hub Open Service** is authorized. If it is not offered
   there, add it later under the project's **Service API** tab
   (**Go to Authorize**).

## 2. Copy the keys

On the project's **Overview** tab, copy **Access ID/Client ID** and
**Access Secret/Client Secret** into `~/.config/remote-control/credentials.toml`:

```toml
region = "us"
access_id = "..."
access_secret = "..."
```

The file is created with mode 600 by `install.sh`; `rc` refuses to read it if
other users can.

## 3. Link the app account

1. In the project, open **Devices > Link App Account** (sometimes
   **Link Tuya App Account**) and click **Add App Account**. A QR code
   appears.
2. **Try the Positivo app first.** In Positivo Casa Inteligente, find the
   scan icon (usually on the **Me** / **Perfil** tab, top right) and scan the
   QR code. Choose automatic link.
3. Refresh the **All Devices** list. If the IR blaster appears, skip to step 4.

**If the Positivo app cannot scan the code, or no devices appear**, Tuya only
links its own apps. Move the blaster to Smart Life:

1. In the Positivo app, remove the IR blaster (this also removes its remotes).
2. Install **Smart Life** on the iPhone, create an account in Brazil, and add
   the blaster (put it in pairing mode as the manual says).
3. In Smart Life, open the blaster, **add a remote > Air conditioner > Komeco**,
   and check that the app can turn the AC on and off.
4. For Alexa: in the Alexa app, enable the **Smart Life** skill and
   discover devices again.
5. Scan the QR code from step 3.1 with Smart Life (**Me > scan icon**).

## 4. Find the device and remote ids

1. In **All Devices**, copy the **Device ID** of the IR blaster into
   `credentials.toml`:

   ```toml
   [remotes.ac]
   infrared_id = "..."
   ```

2. Run:

   ```sh
   rc tuya remotes
   ```

   It prints a line like `remote_id = "..." # Komeco ... (air conditioner)`.
   Copy that `remote_id` under `[remotes.ac]`.

## 5. Start and test

```sh
systemctl --user restart remote-control
rc status           # no "setup needed" any more
rc ac on
rc ac temp 23
rc ac off
```

The red warning in the bar goes away once the daemon has credentials.

Please also run these two and keep the output. They show whether the cloud
reports changes made from the phone or Alexa, and whether your Komeco remote
exposes a swing key:

```sh
rc ac fetch && rc status
rc tuya keys
```

## The free plan expires

The project starts on a free trial of the cloud services. When it lapses,
every command fails with a permission or subscription error from Tuya, and
the bar shows the red warning. Extend it for free under
**Cloud > Cloud Services** (or the **Service API** tab) by applying for an
extension of the trial edition.
