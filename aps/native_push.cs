// ABI from the installed iCloud 15.8 APSDaemon type library. No Apple binaries
// or credentials are embedded here. This bridge uses the registered COM service.
using System;
using System.Text;
using System.Runtime.InteropServices;
using System.Runtime.InteropServices.ComTypes;

namespace AppleServicesNativePush {
    [ComImport, Guid("AD9E96D1-B5AF-4F42-82C1-95CB38164E60"), InterfaceType(ComInterfaceType.InterfaceIsDual)]
    interface IAPSServer {
        void InitializeEnvironment([MarshalAs(UnmanagedType.BStr)] string environment);
        [return: MarshalAs(UnmanagedType.BStr)] string CopyPublicToken();
        void AddTopic(int list, [MarshalAs(UnmanagedType.BStr)] string topic);
        void RemoveTopic(int list, [MarshalAs(UnmanagedType.BStr)] string topic);
        void Clear(int list);
    }

    [ComImport, Guid("5312BE26-34D9-432D-A03E-1732E463BC4D"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    interface INotificationCenter {
        void PostEvent([MarshalAs(UnmanagedType.BStr)] string topic, [MarshalAs(UnmanagedType.BStr)] string message);
    }

    [Guid("CE9691B7-616F-4C69-A74E-B6E701E3FD1B"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown), ComVisible(true)]
    public interface INotificationEvents {
        void OnEvent([MarshalAs(UnmanagedType.BStr)] string topic, [MarshalAs(UnmanagedType.BStr)] string message);
    }

    [ClassInterface(ClassInterfaceType.None), ComVisible(true)]
    public sealed class Listener : INotificationEvents {
        readonly string probeTopic;
        public Listener(string probeTopic) { this.probeTopic = probeTopic; }
        public void OnEvent(string topic, string message) {
            if (topic == probeTopic && message == "local callback test") {
                Bridge.Write("{\"type\":\"selftest\"}");
                return;
            }
            if (topic != Bridge.Topic) return;
            if (message == null || message.Length > 1024 * 1024) {
                Bridge.Write("{\"type\":\"error\",\"stage\":\"payload-size\"}");
                return;
            }
            string payload = Convert.ToBase64String(Encoding.UTF8.GetBytes(message));
            Bridge.Write("{\"type\":\"event\",\"payload\":\"" + payload + "\"}");
        }
    }

    public static class Bridge {
        internal const string Topic = "com.apple.idmsauth";
        static readonly object outputLock = new object();
        internal static void Write(string line) {
            lock (outputLock) { Console.WriteLine(line); Console.Out.Flush(); }
        }
        public static void Run(bool probe) {
            object serverObject = null, centerObject = null;
            IConnectionPoint point = null;
            IAPSServer server = null;
            Listener listener = null;
            int cookie = 0;
            bool subscribed = false;
            string stage = "create-service";
            try {
                serverObject = Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("6812639B-FD61-4329-9901-22CFDBD690FE")));
                server = (IAPSServer)serverObject;
                stage = "initialize";
                server.InitializeEnvironment("production");
                stage = "token";
                byte[] token = Convert.FromBase64String(server.CopyPublicToken());
                if (token.Length != 32) throw new InvalidOperationException();
                stage = "notification-center";
                centerObject = Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("FDD068C2-D51A-4175-8A20-5CBC704EA3BD")));
                Guid eventID = new Guid("CE9691B7-616F-4C69-A74E-B6E701E3FD1B");
                ((IConnectionPointContainer)centerObject).FindConnectionPoint(ref eventID, out point);
                string probeTopic = probe ? "appleservices.native.selftest." + Guid.NewGuid().ToString("N") : null;
                listener = new Listener(probeTopic);
                stage = "callback";
                point.Advise(listener, out cookie);
                stage = "subscribe";
                // List 0 is enabled topics, confirmed in APSSetEnabledTopicsC.
                // Never clear the daemon's lists or subscribe to unrelated topics.
                server.AddTopic(0, Topic);
                subscribed = true;
                Write("{\"type\":\"ready\",\"token\":\"" + Convert.ToBase64String(token) + "\"}");
                if (probe) {
                    // A private local event verifies COM delivery, not Apple push.
                    ((INotificationCenter)centerObject).PostEvent(probeTopic, "local callback test");
                }
                while (Console.ReadLine() != null) { }
            } catch (Exception error) {
                Write("{\"type\":\"error\",\"stage\":\"" + stage + "\",\"hresult\":\"" + error.HResult.ToString("X8") + "\"}");
            } finally {
                if (subscribed) { try { server.RemoveTopic(0, Topic); } catch { } }
                if (cookie != 0) { try { point.Unadvise(cookie); } catch { } }
                GC.KeepAlive(listener);
                if (point != null) Marshal.ReleaseComObject(point);
                if (centerObject != null) Marshal.ReleaseComObject(centerObject);
                if (serverObject != null) Marshal.ReleaseComObject(serverObject);
            }
        }
    }
}
